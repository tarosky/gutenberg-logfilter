package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

func buildLogFilter() {
	cmd := exec.Command("go", "build", "-o", "test/work/logfilter", ".")
	cmd.Stderr = os.Stderr
	fmt.Println(cmd)
	if err := cmd.Run(); err != nil {
		panic(err)
	}
}

func initIntegratedTestSuite() *IntegratedTestSuite {
	fixWorkDir()
	buildLogFilter()
	return &IntegratedTestSuite{
		ctx: context.Background(),
	}
}

// IntegratedTestSuite holds configs and sessions required to execute program.
type IntegratedTestSuite struct {
	suite.Suite
	ctx      context.Context
	process  *os.Process
	stdout   io.Writer
	stderr   io.Writer
	signaled bool
}

func TestIntegratedSuite(t *testing.T) {
	s := initIntegratedTestSuite()
	suite.Run(t, s)
}

func (s *IntegratedTestSuite) SetupTest() {
	removeTestFiles(&s.Suite)
	createTestFIFOs(&s.Suite)

	cmd := exec.CommandContext(s.ctx, "test/work/logfilter", "-c", string(configPath))
	{
		var err error
		s.stdout, err = os.OpenFile("test/work/stdout.log", os.O_WRONLY|os.O_CREATE, 0644)
		s.Assert().NoError(err)
		cmd.Stdout = s.stdout
	}
	{
		var err error
		s.stderr, err = os.OpenFile("test/work/stderr.log", os.O_WRONLY|os.O_CREATE, 0644)
		s.Assert().NoError(err)
		cmd.Stderr = s.stderr
	}
	s.Require().NoError(cmd.Start())
	s.process = cmd.Process
	s.signaled = false
}

func (s *IntegratedTestSuite) Sigterm() {
	if s.signaled {
		return
	}

	_ = s.process.Signal(unix.SIGTERM)
	state, err := s.process.Wait()
	if err != nil && err.Error() != "os: process already finished" {
		s.Require().NoError(err)
	}
	s.Require().Contains([]int{0, -1}, state.ExitCode())
	s.signaled = true
}

func (s *IntegratedTestSuite) TearDownTest() {
	s.Sigterm()
}

func (s *IntegratedTestSuite) TestSendLog() {
	f, err := os.OpenFile(string(testFIFO1), os.O_WRONLY|os.O_APPEND, 0644)
	s.Assert().NoError(err)
	defer f.Close()

	f.WriteString("TEXT: foo bar\n")

	time.Sleep(time.Second)

	f2, err := os.Open(string(testLogPath1) + ".gu")
	s.Assert().NoError(err)

	bs, err := ioutil.ReadAll(f2)
	s.Require().NoError(err)

	s.Assert().Equal("{\"message\": \"foo bar\"}\n", string(bs))
}

func (s *IntegratedTestSuite) TestStateFile() {
	f1, err := os.OpenFile(string(testFIFO1), os.O_WRONLY|os.O_APPEND, 0644)
	s.Assert().NoError(err)
	defer f1.Close()
	f2, err := os.OpenFile(string(testFIFO2), os.O_WRONLY|os.O_APPEND, 0644)
	s.Assert().NoError(err)
	defer f2.Close()

	for i := 0; i < 15; i++ {
		f1.WriteString("foo bar\n")
	}

	for i := 0; i < 5; i++ {
		f2.WriteString("foo bar\n")
	}

	time.Sleep(time.Second)

	s.Sigterm()

	js, err := ioutil.ReadFile(string(statePath))
	s.Require().NoError(err)

	m := map[string]map[string]map[string]int{}
	s.Assert().NoError(json.Unmarshal(js, &m))

	testLogAbsPath1, err := filepath.Abs(string(testLogPath1))
	s.Require().NoError(err)
	testLogAbsPath2, err := filepath.Abs(string(testLogPath2))
	s.Require().NoError(err)

	s.Assert().Len(m, 1)
	s.Assert().Len(m["logs"], 2)
	s.Assert().Equal(m["logs"][testLogAbsPath1]["rotation"], 1)
	s.Assert().Equal(m["logs"][testLogAbsPath1]["count"], 5)
	s.Assert().Equal(m["logs"][testLogAbsPath2]["rotation"], 0)
	s.Assert().Equal(m["logs"][testLogAbsPath2]["count"], 5)
}

func (s *IntegratedTestSuite) TestStateFileCloseEarly() {
	f1, err := os.OpenFile(string(testFIFO1), os.O_WRONLY|os.O_APPEND, 0644)
	s.Assert().NoError(err)

	for i := 0; i < 15; i++ {
		f1.WriteString("foo bar\n")
	}

	f1.Close()

	time.Sleep(time.Second)

	s.Sigterm()

	fs, err := os.Open(string(statePath))
	s.Assert().NoError(err)
	defer fs.Close()
	js, err := ioutil.ReadAll(fs)
	s.Require().NoError(err)

	m := map[string]map[string]map[string]int{}
	s.Assert().NoError(json.Unmarshal(js, &m))

	testLogAbsPath1, err := filepath.Abs(string(testLogPath1))
	s.Require().NoError(err)
	testLogAbsPath2, err := filepath.Abs(string(testLogPath2))
	s.Require().NoError(err)

	s.Assert().Len(m, 1)
	s.Assert().Len(m["logs"], 2)
	s.Assert().Equal(m["logs"][testLogAbsPath1]["rotation"], 1)
	s.Assert().Equal(m["logs"][testLogAbsPath1]["count"], 5)
	s.Assert().Equal(m["logs"][testLogAbsPath2]["rotation"], 0)
	s.Assert().Equal(m["logs"][testLogAbsPath2]["count"], 0)
}
