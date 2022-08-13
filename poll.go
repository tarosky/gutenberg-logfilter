package main

import (
	"errors"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

type poller struct {
	log        *myLogger
	source     AbsolutePath
	epfd       int
	fifoFD     int
	donePipe   [2]int
	senderDone bool
	waiterDone bool
}

func newPoller(log *myLogger, source AbsolutePath, fifoFD int) (p *poller, e error) {
	epfd, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if epfd == -1 {
		return nil, err
	}
	defer func() {
		if e != nil {
			unix.Close(epfd)
		}
	}()

	donePipe := [2]int{}
	if err := unix.Pipe2(donePipe[:], unix.O_NONBLOCK|unix.O_CLOEXEC); err != nil {
		return nil, err
	}
	defer func() {
		if e != nil {
			unix.Close(donePipe[0])
			unix.Close(donePipe[1])
		}
	}()

	fifoEvent := &unix.EpollEvent{
		Events: unix.EPOLLIN | unix.EPOLLET,
		Fd:     int32(fifoFD),
	}
	if err := unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, fifoFD, fifoEvent); err != nil {
		return nil, err
	}

	doneEvent := &unix.EpollEvent{
		Events: unix.EPOLLIN,
		Fd:     int32(donePipe[0]), // Read end of pipe
	}
	if err := unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, donePipe[0], doneEvent); err != nil {
		return nil, err
	}

	return &poller{
		log:        log,
		source:     source,
		epfd:       epfd,
		fifoFD:     fifoFD,
		donePipe:   donePipe,
		senderDone: false,
		waiterDone: false,
	}, nil
}

func (p *poller) wait() (bool, bool, error) {
	if p.waiterDone {
		return false, true, nil
	}

	zapSource := zap.String("source", string(p.source))
	genericError := errors.New("epoll failure")

	events := make([]unix.EpollEvent, 2)
	var (
		n   int
		err error
	)
	for {
		n, err = unix.EpollWait(p.epfd, events, -1)
		if err != unix.EINTR {
			break
		}
		// Ignore signal interruption.
	}
	if err != nil {
		p.log.Warn("epoll_wait() returned error", zapSource, zap.Error(err))
		return false, false, genericError
	}
	if n == -1 {
		p.log.Warn("-1 returned without errno", zapSource)
		return false, false, genericError
	}
	if n == 0 {
		p.log.Warn(
			"zero events returned by epoll_wait() should never happen",
			zapSource)
		return false, false, genericError
	}

	fifoin := false
	e := error(nil)
	for _, event := range events[:n] {
		switch event.Fd {
		case int32(p.fifoFD):
			if event.Events&unix.EPOLLERR != 0 {
				p.log.Warn("EPOLLERR occurred", zapSource, zap.String("fd", "fifo"))
				e = genericError
			}
			if event.Events&unix.EPOLLIN != 0 {
				fifoin = true
			}
		case int32(p.donePipe[0]):
			// // Write side of donePipe has been closed.
			// // We can safely ignore it.
			// if event.Events&unix.EPOLLHUP != 0 {
			// }

			if event.Events&unix.EPOLLERR != 0 {
				p.log.Warn("EPOLLERR occurred", zapSource, zap.String("fd", "done"))
				e = genericError
			}
			if event.Events&unix.EPOLLIN != 0 {
				p.waiterDone = true
			}
		}
	}

	// p.log.Debug(
	// 	"epoll returns",
	// 	zapSource, zap.Bool("ok", fifoin), zap.Bool("done", p.waiterDone), zap.Error(e))

	return fifoin, p.waiterDone, e
}

func (p *poller) writeDone() error {
	if p.senderDone {
		return nil
	}

	buf := make([]byte, 1)
	var err error
	_, err = unix.Write(p.donePipe[1], buf)
	if err != nil {
		return err
	}

	p.senderDone = true

	return nil
}

func (p *poller) closeWrite() {
	zapSource := zap.String("source", string(p.source))
	if err := unix.Close(p.donePipe[1]); err != nil {
		p.log.Warn("failed to close done pipe (write end)", zapSource, zap.Error(err))
	}
}

func (p *poller) closeReceive() {
	zapSource := zap.String("source", string(p.source))
	if err := unix.Close(p.donePipe[0]); err != nil {
		p.log.Warn("failed to close done pipe (read end)", zapSource, zap.Error(err))
	}
}
