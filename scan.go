package main

import (
	"bytes"
	"io"
)

type scanner struct {
	r        io.Reader
	line     string
	buf      []byte
	start    int
	end      int
	err      error
	reWait   bool
	draining bool
	log      *myLogger
}

const truncatedSign = "...[truncated]"

func newScanner(log *myLogger, r io.Reader, maxLineLen int) *scanner {
	return &scanner{
		r:   r,
		log: log,
		buf: make([]byte, maxLineLen),
	}
}

func (s *scanner) error() error {
	if s.err == io.EOF {
		return nil
	}
	return s.err
}

func (s *scanner) text() string {
	return s.line
}

func (s *scanner) scan() bool {
	switch s.err {
	case io.EOF:
		// Clear EOF to read new data
		s.setErr(nil)
	case nil:
	default:
		return false
	}

	for {
		// Cut off a line from buffer if possible.
		if s.start < s.end {
			advance, line := s.split(s.buf[s.start:s.end])
			s.start += advance
			if line != nil {
				if s.draining {
					s.draining = false
					continue
				}
				s.line = string(line)
				return true
			}
		}
		// Move content in the buffer to the head
		if 0 < s.start && (s.end == len(s.buf) || len(s.buf)/2 < s.start) {
			copy(s.buf, s.buf[s.start:s.end])
			s.end -= s.start
			s.start = 0
		}

		if s.end == len(s.buf) {
			// Buffer is full. Start or keep draining.
			if s.draining {
				// Keep draining.
				s.end = 0
				continue
			}
			s.line = string(s.buf) + truncatedSign
			s.end = 0
			s.draining = true
			return true
		}

		if s.reWait {
			s.reWait = false
			return false
		}
		n, err := s.r.Read(s.buf[s.end:len(s.buf)])
		if err == io.EOF || n < len(s.buf)-s.end {
			// No data available at this time.
			// Wait until new data is available.
			s.reWait = true
		}
		// s.log.Debug("read", zap.Int("n", n), zap.Error(err))

		s.end += n
		if err != nil {
			s.setErr(err)
		}
	}
}

func (s *scanner) setErr(err error) {
	if s.err == nil || s.err == io.EOF {
		s.err = err
	}
}

func (s *scanner) split(data []byte) (advance int, line []byte) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[0:i]
	}
	return 0, nil
}
