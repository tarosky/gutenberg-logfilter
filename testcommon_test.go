package main

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/stretchr/testify/suite"
)

var (
	configPath   = mustGetAbsPath("test/config.json")
	testLogPath1 = mustGetAbsPath("test/work/test1.log")
	testLogPath2 = mustGetAbsPath("test/work/test2.log")
	testFIFO1    = mustGetAbsPath("test/work/test1.fifo")
	testFIFO2    = mustGetAbsPath("test/work/test2.fifo")
	statePath    = mustGetAbsPath("test/work/state.json")
	logPath      = mustGetAbsPath("test/work/logfilter.log.json")
	errorLogPath = mustGetAbsPath("test/work/logfilter.error.log")
	pidPath      = mustGetAbsPath("test/work/logfilter.pid")
)

const (
	enoughLineBuffer = 1024
	testMaxLineLen   = 100
	testMaxLines     = 100
)

func mustGetAbsPath(path string) AbsolutePath {
	absPath, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}

	return AbsolutePath(absPath)
}

// fixWorkDir moves working directory to project root directory.
// https://brandur.org/fragments/testing-go-project-root
func fixWorkDir() {
	_, filename, _, _ := runtime.Caller(0)
	dir := path.Join(path.Dir(filename), ".")
	err := os.Chdir(dir)
	if err != nil {
		panic(err)
	}
}

func removeTestFiles(s *suite.Suite) {
	s.Require().NoError(os.RemoveAll(string(testLogPath1) + ".gu"))
	s.Require().NoError(os.RemoveAll(string(testLogPath1) + ".chi"))
	s.Require().NoError(os.RemoveAll(string(testLogPath1) + ".pa"))
	s.Require().NoError(os.RemoveAll(string(testLogPath2) + ".gu"))
	s.Require().NoError(os.RemoveAll(string(testLogPath2) + ".chi"))
	s.Require().NoError(os.RemoveAll(string(testLogPath2) + ".pa"))
	s.Require().NoError(os.RemoveAll(string(testFIFO1)))
	s.Require().NoError(os.RemoveAll(string(testFIFO2)))
	s.Require().NoError(os.RemoveAll(string(statePath)))
	s.Require().NoError(os.RemoveAll(string(logPath)))
	s.Require().NoError(os.RemoveAll(string(errorLogPath)))
	s.Require().NoError(os.RemoveAll(string(pidPath)))
}

func createTestFIFOs(s *suite.Suite) {
	s.Require().NoError(syscall.Mkfifo(string(testFIFO1), 0644))
	s.Require().NoError(syscall.Mkfifo(string(testFIFO2), 0644))
}

func sleepABit() {
	time.Sleep(30 * time.Millisecond)
}
