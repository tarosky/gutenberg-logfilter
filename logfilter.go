package main

import (
	"context"
	"encoding/json"
	"fmt"
	stdlog "log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/daemon"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
)

// Provided by govvv at compile time
var Version string

const (
	inputBufferSize  = 1024
	outputBufferSize = 1024
	retryInterval    = 5 * time.Second
)

// This implements zapcore.WriteSyncer interface.
type lockedFileWriteSyncer struct {
	m    sync.Mutex
	f    *os.File
	path string
}

func newLockedFileWriteSyncer(path string) *lockedFileWriteSyncer {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while creating log file: path: %s", err.Error())
		panic(err)
	}

	return &lockedFileWriteSyncer{
		f:    f,
		path: path,
	}
}

func (s *lockedFileWriteSyncer) Write(bs []byte) (int, error) {
	s.m.Lock()
	defer s.m.Unlock()

	return s.f.Write(bs)
}

func (s *lockedFileWriteSyncer) Sync() error {
	s.m.Lock()
	defer s.m.Unlock()

	return s.f.Sync()
}

func (s *lockedFileWriteSyncer) reopen() {
	s.m.Lock()
	defer s.m.Unlock()

	if err := s.f.Close(); err != nil {
		fmt.Fprintf(
			os.Stderr, "error while reopening file: path: %s, err: %s", s.path, err.Error())
	}

	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		fmt.Fprintf(
			os.Stderr, "error while reopening file: path: %s, err: %s", s.path, err.Error())
		panic(err)
	}

	s.f = f
}

func (s *lockedFileWriteSyncer) Close() error {
	s.m.Lock()
	defer s.m.Unlock()

	return s.f.Close()
}

type myLogger struct {
	z *zap.Logger
	v zapcore.Field
}

func (l *myLogger) Debug(msg string, fields ...zap.Field) {
	fs := make([]zap.Field, 0, len(fields)+1)
	fs = append(fs, l.v)
	fs = append(fs, fields...)
	l.z.Debug(msg, fs...)
}

func (l *myLogger) Info(msg string, fields ...zap.Field) {
	fs := make([]zap.Field, 0, len(fields)+1)
	fs = append(fs, l.v)
	fs = append(fs, fields...)
	l.z.Info(msg, fs...)
}

func (l *myLogger) Warn(msg string, fields ...zap.Field) {
	fs := make([]zap.Field, 0, len(fields)+1)
	fs = append(fs, l.v)
	fs = append(fs, fields...)
	l.z.Warn(msg, fs...)
}

func (l *myLogger) Error(msg string, fields ...zap.Field) {
	fs := make([]zap.Field, 0, len(fields)+1)
	fs = append(fs, l.v)
	fs = append(fs, fields...)
	l.z.Error(msg, fs...)
}

func (l *myLogger) Panic(msg string, fields ...zap.Field) {
	fs := make([]zap.Field, 0, len(fields)+1)
	fs = append(fs, l.v)
	fs = append(fs, fields...)
	l.z.Panic(msg, fs...)
}

func createLogger(ctx context.Context, logPath, errorLogPath AbsolutePath) *myLogger {
	enc := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        zapcore.OmitKey,
		CallerKey:      zapcore.OmitKey,
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  zapcore.OmitKey,
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	})

	out := newLockedFileWriteSyncer(string(logPath))
	errOut := newLockedFileWriteSyncer(string(errorLogPath))

	sigusr1 := make(chan os.Signal, 1)
	signal.Notify(sigusr1, syscall.SIGUSR1)

	go func() {
	loop:
		for {
			select {
			case _, ok := <-sigusr1:
				if !ok {
					break
				}
				out.reopen()
				errOut.reopen()
			case <-ctx.Done():
				signal.Stop(sigusr1)
				// closing sigusr1 causes panic (close of closed channel)
				break loop
			}
		}
	}()

	return &myLogger{
		z: zap.New(
			zapcore.NewCore(enc, out, zap.NewAtomicLevelAt(zap.DebugLevel)),
			zap.ErrorOutput(errOut),
			zap.Development(),
			zap.WithCaller(false)),
		v: zap.String("version", Version),
	}
}

func setPIDFile(e *environment, path string) func() {
	if path == "" {
		return func() {}
	}

	pid := []byte(strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(path, pid, 0644); err != nil {
		e.log.Panic(
			"failed to create PID file",
			zap.String("path", path),
			zap.Error(err))
	}

	return func() {
		if err := os.Remove(path); err != nil {
			e.log.Error(
				"failed to remove PID file",
				zap.String("path", path),
				zap.Error(err))
		}
	}
}

type AbsolutePath string

func (p *AbsolutePath) UnmarshalJSON(data []byte) error {
	var path string
	if err := json.Unmarshal(data, &path); err != nil {
		return err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	*p = AbsolutePath(absPath)

	return nil
}

func (p *AbsolutePath) UnmarshalText(text []byte) error {
	return p.UnmarshalJSON(text)
}

type pfilter interface {
	initialize() error
	try(src string) bool
	process(src string) string
}

type match struct {
	Regex string `json:"regex"`
	regex *regexp.Regexp
}

func (m *match) initialize() error {
	re, err := regexp.Compile(m.Regex)
	if err != nil {
		return err
	}
	m.regex = re
	return nil
}

func (m *match) try(src string) bool {
	return m.regex.MatchString(src)
}

func (m *match) process(src string) string {
	return src
}

type extract struct {
	Regex   string `json:"regex"`
	Printf  string `json:"printf"`
	regex   *regexp.Regexp
	matches []string
}

func (e *extract) initialize() error {
	re, err := regexp.Compile(e.Regex)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("illegal regex: \"%s\"", e.Regex))
	}
	e.regex = re
	return nil
}

func (e *extract) try(src string) bool {
	e.matches = e.regex.FindStringSubmatch(src)
	return e.matches != nil
}

func (e *extract) process(src string) string {
	args := make([]interface{}, 0, len(e.matches))
	for _, m := range e.matches {
		args = append(args, m)
	}
	return fmt.Sprintf(e.Printf, args...)
}

type pfilters []pfilter

func (fs *pfilters) UnmarshalJSON(bs []byte) error {
	rms := []*json.RawMessage{}
	if err := json.Unmarshal(bs, &rms); err != nil {
		return err
	}

	pfs := make([]pfilter, 0, len(rms))
	for _, rm := range rms {
		fmap := map[string]*json.RawMessage{}
		if err := json.Unmarshal(*rm, &fmap); err != nil {
			return err
		}

		if len(fmap) != 1 {
			return fmt.Errorf("illegal config format: %#v", fmap)
		}

	loop:
		for n, j := range fmap {
			switch n {
			case "match":
				m := &match{}
				if err := json.Unmarshal(*j, m); err != nil {
					return err
				}
				pfs = append(pfs, m)
				break loop
			case "extract":
				r := &extract{}
				if err := json.Unmarshal(*j, r); err != nil {
					return err
				}
				pfs = append(pfs, r)
				break loop
			}
			return fmt.Errorf("unknown filter: %s", n)
		}
	}

	*fs = pfs
	return nil
}

func (fs *pfilters) initialize() error {
	for _, f := range *fs {
		if err := f.initialize(); err != nil {
			return err
		}
	}
	return nil
}

type output struct {
	Path AbsolutePath `json:"path"`
}

type route struct {
	Filters pfilters `json:"filters"`
	Output  string   `json:"output"`
}

func (r *route) initialize() error {
	return r.Filters.initialize()
}

type configure struct {
	Source     AbsolutePath      `json:"source"`
	Routes     []route           `json:"routes"`
	Outputs    map[string]output `json:"outputs"`
	MaxLines   int               `json:"maxlines"`
	MaxLineLen int               `json:"maxlinelen"`
	StateFile  AbsolutePath      `json:"statefile"`
	PIDFile    AbsolutePath      `json:"pidfile"`
	MyLog      AbsolutePath      `json:"mylog"`
	MyErrorLog AbsolutePath      `json:"myerrorlog"`
}

func (c *configure) initialize() error {
	for _, r := range c.Routes {
		if err := r.initialize(); err != nil {
			return err
		}
	}
	return nil
}

type environment struct {
	configure
	log   *myLogger
	state *state
}

func main() {
	cliMain(context.Background(), os.Args)
}

func cliMain(ctx context.Context, args []string) {
	app := cli.NewApp()
	app.Name = "logfilter"
	app.Description = "Get logs via named pipe, filter them, and pass them to CloudWatch Agent"

	app.Flags = []cli.Flag{
		&cli.PathFlag{
			Name:     "config",
			Aliases:  []string{"c"},
			Required: true,
			Usage:    "File to configure logfilter.",
		},
	}

	app.Action = func(c *cli.Context) error {
		mustGetAbsPath := func(name string) string {
			path, err := filepath.Abs(c.Path(name))
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to get %s: %s", name, err.Error())
				panic(err)
			}
			return path
		}

		configPath := mustGetAbsPath("config")
		configData, err := os.ReadFile(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read config data: %s", err.Error())
			panic(err)
		}

		cfg := &configure{}
		if err := json.Unmarshal(configData, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "malformed config file: %s", err.Error())
			panic(err)
		}

		if err := cfg.initialize(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to initialize from config: %s", err.Error())
			panic(err)
		}

		state := loadState(cfg.StateFile)

		ctx, cancel := context.WithCancel(c.Context)

		e := &environment{
			configure: *cfg,
			log:       createLogger(c.Context, cfg.MyLog, cfg.MyErrorLog),
			state:     state,
		}
		defer e.log.z.Sync()

		removePIDFile := setPIDFile(e, string(cfg.PIDFile))
		defer removePIDFile()

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
		go func() {
			defer func() {
				signal.Stop(sig)
				close(sig)
			}()

			select {
			case <-ctx.Done():
			case <-sig:
				cancel()
			}
		}()

		_, _ = daemon.SdNotify(false, daemon.SdNotifyReady)

		e.log.Info("loading config done")
		return run(ctx, e)
	}

	err := app.RunContext(ctx, args)
	if err != nil {
		stdlog.Panic("failed to run app", zap.Error(err))
	}
}

type outputStatePair struct {
	path  AbsolutePath
	state *outputState
}

func run(ctx context.Context, e *environment) error {
	stateCh := make(chan *outputStatePair)
	done := make(chan *state)

	go func() {
		outputs := make(map[AbsolutePath]*outputState, len(e.Outputs))
		for pair := range stateCh {
			outputs[pair.path] = pair.state
		}

		done <- &state{Outputs: outputs}
		close(done)
	}()

	inputCh := make(chan string, inputBufferSize)
	outputsIn := make(map[string]chan<- string, len(e.Outputs))
	outputsOut := make(map[string]<-chan string, len(e.Outputs))
	for n := range e.Outputs {
		ch := make(chan string, outputBufferSize)
		outputsOut[n] = ch
		outputsIn[n] = ch
	}

	grp := &errgroup.Group{}

	grp.Go(func() error {
		return processInput(ctx, e.log, inputCh, e.MaxLineLen, e.Source)
	})

	grp.Go(func() error {
		return processRouting(e.log, inputCh, e.Routes, outputsIn)
	})

	for name, output := range e.Outputs {
		name := name
		output := output
		grp.Go(func() error {
			st, err := processOutput(e.log, outputsOut[name], output.Path, e.state.Outputs[output.Path], e.MaxLines)
			stateCh <- &outputStatePair{
				path:  output.Path,
				state: st,
			}
			return err
		})
	}

	grperr := grp.Wait()
	close(stateCh)

	if err := saveState(e.log, e.StateFile, <-done); err != nil {
		return err
	}

	return grperr
}

type state struct {
	Outputs map[AbsolutePath]*outputState `json:"outputs"`
}

type outputState struct {
	Rotation int `json:"rotation"`
	Count    int `json:"count"`
}

func loadState(path AbsolutePath) *state {
	stateData, err := os.ReadFile(string(path))
	if err != nil {
		if os.IsNotExist(err) {
			stateData = []byte("{}")
		} else {
			fmt.Fprintf(os.Stderr, "failed to open state file: %s", err.Error())
			panic(err)
		}
	}

	state := &state{}
	if err := json.Unmarshal(stateData, state); err != nil {
		fmt.Fprintf(os.Stderr, "failed to unmarshal state data: %s", err.Error())
		panic(err)
	}

	if state.Outputs == nil {
		state.Outputs = map[AbsolutePath]*outputState{}
	}

	return state
}

func saveState(log *myLogger, path AbsolutePath, state *state) error {
	file, err := os.OpenFile(string(path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Error("failed to open state file for writing", zap.String("path", string(path)), zap.Error(err))
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("failed to close file", zap.String("path", string(path)), zap.Error(err))
		}
	}()

	bs, err := json.Marshal(state)
	if err != nil {
		log.Error("failed to marshal state data", zap.String("path", string(path)), zap.Error(err))
		return err
	}

	{
		var err error
		if _, err = file.Write(bs); err != nil {
			log.Error("failed to write state data", zap.String("path", string(path)), zap.Error(err))
			return err
		}
	}
	log.Info("state file saved", zap.String("path", string(path)))

	return nil
}

func processInput(
	ctx context.Context,
	log *myLogger,
	inputCh chan<- string,
	maxLineLen int,
	source AbsolutePath,
) error {
	log.Info("reading from fifo started", zap.String("path", string(source)))
	defer close(inputCh)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			doProcessInput(ctx, log, inputCh, maxLineLen, source)
		}
	}
}

func doProcessInput(
	ctx context.Context,
	log *myLogger,
	inputCh chan<- string,
	maxLineLen int,
	source AbsolutePath,
) {
	zapSource := zap.String("source", string(source))

	file, err := os.OpenFile(string(source), syscall.O_RDONLY|syscall.O_NONBLOCK, 0600)
	if err != nil {
		log.Error("failed to open file", zapSource, zap.Error(err))
		time.Sleep(retryInterval)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Error("failed to close file", zapSource, zap.Error(err))
		}
	}()

	poller, err := newPoller(log, source, int(file.Fd()))
	if err != nil {
		log.Error("failed to create new poller", zapSource, zap.Error(err))
		time.Sleep(retryInterval)
		return
	}
	defer poller.closeReceive()

	aborted := make(chan struct{})

	go func() {
		defer poller.closeWrite()

		select {
		case <-ctx.Done():
			log.Debug("goroutine done called", zapSource)
			poller.writeDone()
		case <-aborted:
			log.Debug("goroutine aborted called", zapSource)
		}
	}()

	scan := newScanner(log, file, maxLineLen)

	for {
		ok, done, err := poller.wait()
		if done {
			return
		}
		if err != nil {
			log.Error("epoll error", zapSource, zap.Error(err))
			close(aborted)
			time.Sleep(retryInterval)
			return
		}
		if !ok {
			continue
		}

		for scan.scan() {
			// log.Debug("scanning")
			inputCh <- scan.text()
		}
		// log.Debug("scan finished")
		if err := scan.error(); err != nil {
			log.Warn("error during scanning", zapSource, zap.Error(err))
			close(aborted)
			time.Sleep(retryInterval)
			return
		}
	}
}

func processRouting(
	_ *myLogger,
	inputCh <-chan string,
	routes []route,
	outputCh map[string]chan<- string,
) error {
	defer func() {
		for _, ch := range outputCh {
			close(ch)
		}
	}()

	for line := range inputCh {
		name, l := processLine(line, routes)
		if name == nil {
			continue
		}
		outputCh[*name] <- l
	}
	return nil
}

func processLine(line string, routes []route) (*string, string) {
outer:
	for _, route := range routes {
		l := line
		for _, f := range route.Filters {
			if !f.try(l) {
				continue outer
			}
			l = f.process(l)
		}
		return &route.Output, l
	}

	// No matching routes
	return nil, ""
}

func processOutput(
	log *myLogger,
	outputCh <-chan string,
	output AbsolutePath,
	state *outputState,
	maxLines int,
) (*outputState, error) {
	suffixes := []string{".gu", ".chi", ".pa"}

	lineCount := 0
	rotation := 0
	if state != nil {
		lineCount = state.Count
		rotation = state.Rotation
	}

	var file *os.File

	defer func() {
		if file == nil {
			return
		}
		if err := file.Close(); err != nil {
			log.Warn("failed to close file", zap.Error(err))
		}
	}()

	nextRotation := func() int {
		n := rotation + 1
		if len(suffixes) <= n {
			return 0
		}
		return n
	}

	filePath := func(rotation int) string {
		return string(output) + suffixes[rotation]
	}

	openFile := func() bool {
		path := filePath(rotation)
		nextPath := filePath(nextRotation())

		if err := os.Remove(nextPath); err != nil {
			if !os.IsNotExist(err) {
				log.Error(
					"failed to remove file",
					zap.String("path", nextPath), zap.Error(err))
				return false
			}
		}

		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Error(
				"failed to open file",
				zap.String("path", path), zap.Error(err))
			return false
		}
		file = f
		log.Debug("output file", zap.String("path", path))
		return true
	}

	switchFile := func() bool {
		if file != nil {
			if err := file.Close(); err != nil {
				log.Warn("failed to close file", zap.Error(err))
				// Keep running as much as possible.
			}
		}

		return openFile()
	}

	if !openFile() {
		return nil, fmt.Errorf("failed to start output: %s", filePath(rotation))
	}

	for line := range outputCh {
		if maxLines <= lineCount {
			lineCount = 0
			rotation = nextRotation()
			if !switchFile() {
				log.Info("transitioned to drain mode", zap.String("path", string(output)))
				break
			}
		}

		var err error
		if _, err = fmt.Fprintln(file, line); err != nil {
			log.Warn("failed to write a log line", zap.String("path", filePath(rotation)))
			continue
		}
		lineCount++
	}

	newState := &outputState{
		Rotation: rotation,
		Count:    lineCount,
	}

	select {
	case _, ok := <-outputCh:
		if !ok {
			return newState, nil
		}
	default:
	}

	// Drain mode
	for range outputCh {
	}

	return newState, nil
}
