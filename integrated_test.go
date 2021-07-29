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
	f, err := os.OpenFile(string(testFIFO), os.O_WRONLY|os.O_APPEND, 0644)
	s.Assert().NoError(err)
	defer f.Close()

	f.WriteString("TYPE2: foo bar\n")

	time.Sleep(time.Second)

	f2, err := os.Open(string(testLogPathType2) + ".gu")
	s.Assert().NoError(err)

	bs, err := ioutil.ReadAll(f2)
	s.Require().NoError(err)

	s.Assert().Equal("{\"message\": \"foo bar\"}\n", string(bs))
}

func (s *IntegratedTestSuite) TestStateFile() {
	f, err := os.OpenFile(string(testFIFO), os.O_WRONLY|os.O_APPEND, 0644)
	s.Assert().NoError(err)
	defer f.Close()

	for i := 0; i < 15; i++ {
		f.WriteString("TYPE1: {foo bar}\n")
	}
	for i := 0; i < 5; i++ {
		f.WriteString("TYPE2: baz\n")
	}

	time.Sleep(time.Second)

	s.Sigterm()

	js, err := ioutil.ReadFile(string(statePath))
	s.Require().NoError(err)

	m := map[string]map[string]map[string]int{}
	s.Assert().NoError(json.Unmarshal(js, &m))

	testLogAbsPathType1, err := filepath.Abs(string(testLogPathType1))
	s.Require().NoError(err)
	testLogAbsPathType2, err := filepath.Abs(string(testLogPathType2))
	s.Require().NoError(err)

	s.Assert().Len(m, 1)
	s.Assert().Len(m["outputs"], 2)
	s.Assert().Equal(1, m["outputs"][testLogAbsPathType1]["rotation"])
	s.Assert().Equal(5, m["outputs"][testLogAbsPathType1]["count"])
	s.Assert().Equal(0, m["outputs"][testLogAbsPathType2]["rotation"])
	s.Assert().Equal(5, m["outputs"][testLogAbsPathType2]["count"])
}

func (s *IntegratedTestSuite) TestStateFileCloseEarly() {
	f, err := os.OpenFile(string(testFIFO), os.O_WRONLY|os.O_APPEND, 0644)
	s.Assert().NoError(err)

	for i := 0; i < 15; i++ {
		f.WriteString("TYPE1: {foo bar}\n")
	}

	f.Close()

	time.Sleep(time.Second)

	s.Sigterm()

	fs, err := os.Open(string(statePath))
	s.Assert().NoError(err)
	defer fs.Close()
	js, err := ioutil.ReadAll(fs)
	s.Require().NoError(err)

	m := map[string]map[string]map[string]int{}
	s.Assert().NoError(json.Unmarshal(js, &m))

	testLogAbsPathType1, err := filepath.Abs(string(testLogPathType1))
	s.Require().NoError(err)
	testLogAbsPathType2, err := filepath.Abs(string(testLogPathType2))
	s.Require().NoError(err)

	s.Assert().Len(m, 1)
	s.Assert().Len(m["outputs"], 2)
	s.Assert().Equal(1, m["outputs"][testLogAbsPathType1]["rotation"])
	s.Assert().Equal(5, m["outputs"][testLogAbsPathType1]["count"])
	s.Assert().Equal(0, m["outputs"][testLogAbsPathType2]["rotation"])
	s.Assert().Equal(0, m["outputs"][testLogAbsPathType2]["count"])
}
