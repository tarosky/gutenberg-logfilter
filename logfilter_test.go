package main

import (
	"bytes"
	"context"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sync/errgroup"
)

func initLogFilterInputTestSuite() *LogFilterInputTestSuite {
	fixWorkDir()

	ctx := context.Background()

	return &LogFilterInputTestSuite{ctx: ctx}
}

// LogFilterInputTestSuite holds configs and sessions required to execute program.
type LogFilterInputTestSuite struct {
	suite.Suite
	ctx context.Context
	log *myLogger
}

func TestLogFilterInputSuite(t *testing.T) {
	s := initLogFilterInputTestSuite()
	suite.Run(t, s)
}

func (s *LogFilterInputTestSuite) SetupTest() {
	removeTestFiles(&s.Suite)
	createTestFIFOs(&s.Suite)
	s.log = createLogger(s.ctx, logPath, errorLogPath)
}

func (s *LogFilterInputTestSuite) TearDownTest() {
}

func openFIFO(s *suite.Suite) *os.File {
	f, err := os.OpenFile(string(testFIFO), os.O_WRONLY|os.O_APPEND, 0644)
	s.Require().NoError(err)
	return f
}

func (s *LogFilterInputTestSuite) TestOneLine() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()
	f.WriteString("foo\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 1)
	s.Assert().Equal("foo", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestTwoLinesAtOnce() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString("foo\nbar\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 2)
	s.Assert().Equal("foo", <-inputCh)
	s.Assert().Equal("bar", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestTwoLinesWithPause() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString("foo\n")
	sleepABit()
	f.WriteString("bar\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 2)
	s.Assert().Equal("foo", <-inputCh)
	s.Assert().Equal("bar", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestOneLineWithPause() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString("foo")
	sleepABit()
	f.WriteString("bar\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 1)
	s.Assert().Equal("foobar", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestOneLineWithManyPauses() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString("f")
	sleepABit()
	f.WriteString("o")
	sleepABit()
	f.WriteString("o")
	sleepABit()
	f.WriteString("b")
	sleepABit()
	f.WriteString("a")
	sleepABit()
	f.WriteString("r")
	sleepABit()
	f.WriteString("\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 1)
	s.Assert().Equal("foobar", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestOneLineWithoutNewLine() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString("foo")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 0)
}

func (s *LogFilterInputTestSuite) TestEmptyLine() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString("foo\n\n\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 3)
	s.Assert().Equal("foo", <-inputCh)
	s.Assert().Equal("", <-inputCh)
	s.Assert().Equal("", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestLongLine() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString(strings.Repeat("1234567890", 12) + "\n")
	f.WriteString("foo\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 2)
	s.Assert().Equal(strings.Repeat("1234567890", 10)+truncatedSign, <-inputCh)
	s.Assert().Equal("foo", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestLongLongLine() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString(strings.Repeat("1234567890", 30) + "\n")
	f.WriteString("foo\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 2)
	s.Assert().Equal(strings.Repeat("1234567890", 10)+truncatedSign, <-inputCh)
	s.Assert().Equal("foo", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestMaxLine() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	line := strings.Repeat("1234567890", 9) + "123456789"
	f.WriteString(line + "\n")
	f.WriteString("foo\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 2)
	s.Assert().Equal(line, <-inputCh)
	s.Assert().Equal("foo", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestMaxPlus1Line() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	line := strings.Repeat("1234567890", 10)
	f.WriteString(line + "\n")
	f.WriteString("foo\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 2)
	s.Assert().Equal(line+truncatedSign, <-inputCh)
	s.Assert().Equal("foo", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestConsecutiveLongLines() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString(strings.Repeat("abcdefghij", 11) + "\n")
	f.WriteString(strings.Repeat("1234567890", 11) + "\n")
	f.WriteString("foo\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 3)
	s.Assert().Equal(strings.Repeat("abcdefghij", 10)+truncatedSign, <-inputCh)
	s.Assert().Equal(strings.Repeat("1234567890", 10)+truncatedSign, <-inputCh)
	s.Assert().Equal("foo", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestConsecutiveLongLinesWithPauses() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	defer f.Close()

	f.WriteString(strings.Repeat("abcdefghij", 11) + "\n")
	sleepABit()
	f.WriteString(strings.Repeat("1234567890", 11) + "\n")
	sleepABit()
	f.WriteString("foo\n")

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 3)
	s.Assert().Equal(strings.Repeat("abcdefghij", 10)+truncatedSign, <-inputCh)
	s.Assert().Equal(strings.Repeat("1234567890", 10)+truncatedSign, <-inputCh)
	s.Assert().Equal("foo", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestCloseAndOpen() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	f.WriteString("foo\n")
	f.Close()

	sleepABit()

	f2 := openFIFO(&s.Suite)
	f2.WriteString("bar\n")
	f2.Close()

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 2)
	s.Assert().Equal("foo", <-inputCh)
	s.Assert().Equal("bar", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestCloseAndOpenDuringOutputtingALine() {
	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	f := openFIFO(&s.Suite)
	f.WriteString("foo")
	f.Close()

	sleepABit()

	f2 := openFIFO(&s.Suite)
	f2.WriteString("bar\n")
	f2.Close()

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 1)
	s.Assert().Equal("foobar", <-inputCh)
}

func (s *LogFilterInputTestSuite) TestFIFOCreatedAfterStart() {
	s.Require().NoError(os.RemoveAll(string(testFIFO)))

	inputCh := make(chan string, enoughLineBuffer)
	ctx, cancel := context.WithCancel(s.ctx)

	eg := &errgroup.Group{}
	eg.Go(func() error {
		return processInput(ctx, s.log, inputCh, testMaxLineLen, testFIFO)
	})

	sleepABit()

	s.Require().NoError(syscall.Mkfifo(string(testFIFO), 0644))

	f := openFIFO(&s.Suite)
	f.WriteString("foo\n")
	defer f.Close()

	cancel()
	s.Assert().NoError(eg.Wait())

	s.Assert().Len(inputCh, 1)
	s.Assert().Equal("foo", <-inputCh)
}

func initLogFilterRoutingTestSuite() *LogFilterRoutingTestSuite {
	fixWorkDir()

	ctx := context.Background()

	return &LogFilterRoutingTestSuite{ctx: ctx}
}

// LogFilterRoutingTestSuite holds configs and sessions required to execute program.
type LogFilterRoutingTestSuite struct {
	suite.Suite
	ctx context.Context
	log *myLogger
}

func TestLogFilterRoutingSuite(t *testing.T) {
	s := initLogFilterRoutingTestSuite()
	suite.Run(t, s)
}

func (s *LogFilterRoutingTestSuite) SetupTest() {
	s.log = createLogger(s.ctx, logPath, errorLogPath)
}

func (s *LogFilterRoutingTestSuite) TearDownTest() {
}

func (s *LogFilterRoutingTestSuite) TestSimple() {
	routes := []route{
		{
			Filters: []pfilter{
				&match{
					Regex: "^TYPE1: ",
					regex: regexp.MustCompile("^TYPE1: "),
				},
				&extract{
					Regex:  "\\{[^}]*\\}",
					Printf: "%[1]s",
					regex:  regexp.MustCompile(`\{[^}]*\}`),
				},
			},
			Output: "type1",
		},
		{
			Filters: []pfilter{
				&extract{
					Regex:  "^TYPE2: (.*)$",
					Printf: "{\"message\": \"%[2]s\"}",
					regex:  regexp.MustCompile(`^TYPE2: (.*)$`),
				},
			},
			Output: "type2",
		},
	}

	input := make(chan string, enoughLineBuffer)

	outputType1 := make(chan string, enoughLineBuffer)
	outputType2 := make(chan string, enoughLineBuffer)
	outputs := map[string]chan<- string{
		"type1": outputType1,
		"type2": outputType2,
	}

	go processRouting(s.log, input, routes, outputs)

	input <- "TYPE1: {foobar}"
	input <- "TYPE1: {baz}"
	input <- "TYPE1: foobar"
	input <- "TYPE2: foobar"
	input <- "WARN: foobar"
	input <- ""
	close(input)

	sleepABit()

	s.Assert().Equal("{foobar}", <-outputType1)
	s.Assert().Equal("{baz}", <-outputType1)
	s.Assert().Equal("{\"message\": \"foobar\"}", <-outputType2)
	var ok bool
	_, ok = <-outputType1
	s.Assert().False(ok)
	_, ok = <-outputType2
	s.Assert().False(ok)
}

func initLogFilterOutputTestSuite() *LogFilterOutputSuite {
	fixWorkDir()

	ctx := context.Background()

	return &LogFilterOutputSuite{ctx: ctx}
}

// LogFilterOutputSuite holds configs and sessions required to execute program.
type LogFilterOutputSuite struct {
	suite.Suite
	ctx context.Context
	log *myLogger
}

func TestLogFilterOutputSuite(t *testing.T) {
	s := initLogFilterOutputTestSuite()
	suite.Run(t, s)
}

func (s *LogFilterOutputSuite) SetupTest() {
	removeTestFiles(&s.Suite)
	s.log = createLogger(s.ctx, logPath, errorLogPath)
}

func (s *LogFilterOutputSuite) TearDownTest() {
}

func (s *LogFilterOutputSuite) TestSimple() {
	output := make(chan string, enoughLineBuffer)

	var st *outputState
	eg := &errgroup.Group{}
	eg.Go(func() error {
		var err error
		st, err = processOutput(s.log, output, testLogPathType1, nil, testMaxLines)
		return err
	})

	for i := 0; i < 10; i++ {
		output <- strconv.FormatInt(int64(i), 10)
	}
	close(output)

	s.Assert().NoError(eg.Wait())

	s.Assert().Equal(10, st.Count)
	s.Assert().Equal(0, st.Rotation)

	s.Assert().FileExists(string(testLogPathType1) + ".gu")
	s.Assert().NoFileExists(string(testLogPathType1) + ".chi")
	s.Assert().NoFileExists(string(testLogPathType1) + ".pa")

	bs, err := ioutil.ReadFile(string(testLogPathType1) + ".gu")
	s.Require().NoError(err)
	s.Assert().Equal("0\n1\n2\n3\n4\n5\n6\n7\n8\n9\n", string(bs))
}

func (s *LogFilterOutputSuite) TestRotate1() {
	output := make(chan string, enoughLineBuffer)

	var st *outputState
	eg := &errgroup.Group{}
	eg.Go(func() error {
		var err error
		st, err = processOutput(s.log, output, testLogPathType1, nil, testMaxLines)
		return err
	})

	for i := 0; i < 110; i++ {
		output <- strconv.FormatInt(int64(i), 10)
	}
	close(output)

	s.Assert().NoError(eg.Wait())

	s.Assert().Equal(10, st.Count)
	s.Assert().Equal(1, st.Rotation)

	s.Assert().FileExists(string(testLogPathType1) + ".gu")
	s.Assert().FileExists(string(testLogPathType1) + ".chi")
	s.Assert().NoFileExists(string(testLogPathType1) + ".pa")

	bs, err := ioutil.ReadFile(string(testLogPathType1) + ".chi")
	s.Require().NoError(err)
	s.Assert().Equal("100\n101\n102\n103\n104\n105\n106\n107\n108\n109\n", string(bs))
}

func (s *LogFilterOutputSuite) TestRotate2() {
	output := make(chan string, enoughLineBuffer)

	var st *outputState
	eg := &errgroup.Group{}
	eg.Go(func() error {
		var err error
		st, err = processOutput(s.log, output, testLogPathType1, nil, testMaxLines)
		return err
	})

	for i := 0; i < 210; i++ {
		output <- strconv.FormatInt(int64(i), 10)
	}
	close(output)

	s.Assert().NoError(eg.Wait())

	s.Assert().Equal(10, st.Count)
	s.Assert().Equal(2, st.Rotation)

	s.Assert().NoFileExists(string(testLogPathType1) + ".gu")
	s.Assert().FileExists(string(testLogPathType1) + ".chi")
	s.Assert().FileExists(string(testLogPathType1) + ".pa")

	bs, err := ioutil.ReadFile(string(testLogPathType1) + ".pa")
	s.Require().NoError(err)
	s.Assert().Equal("200\n201\n202\n203\n204\n205\n206\n207\n208\n209\n", string(bs))
}

func (s *LogFilterOutputSuite) TestRotate3() {
	output := make(chan string, enoughLineBuffer)

	var st *outputState
	eg := &errgroup.Group{}
	eg.Go(func() error {
		var err error
		st, err = processOutput(s.log, output, testLogPathType1, nil, testMaxLines)
		return err
	})

	for i := 0; i < 310; i++ {
		output <- strconv.FormatInt(int64(i), 10)
	}
	close(output)

	s.Assert().NoError(eg.Wait())

	s.Assert().Equal(10, st.Count)
	s.Assert().Equal(0, st.Rotation)

	s.Assert().FileExists(string(testLogPathType1) + ".gu")
	s.Assert().NoFileExists(string(testLogPathType1) + ".chi")
	s.Assert().FileExists(string(testLogPathType1) + ".pa")

	bs, err := ioutil.ReadFile(string(testLogPathType1) + ".gu")
	s.Require().NoError(err)
	s.Assert().Equal("300\n301\n302\n303\n304\n305\n306\n307\n308\n309\n", string(bs))
}

func (s *LogFilterOutputSuite) TestRotationBorder() {
	output := make(chan string, enoughLineBuffer)

	var st *outputState
	eg := &errgroup.Group{}
	eg.Go(func() error {
		var err error
		st, err = processOutput(s.log, output, testLogPathType1, nil, testMaxLines)
		return err
	})

	for i := 0; i < 100; i++ {
		output <- strconv.FormatInt(int64(i), 10)
	}
	close(output)

	s.Assert().NoError(eg.Wait())

	s.Assert().Equal(100, st.Count)
	s.Assert().Equal(0, st.Rotation)

	s.Assert().FileExists(string(testLogPathType1) + ".gu")
	s.Assert().NoFileExists(string(testLogPathType1) + ".chi")
	s.Assert().NoFileExists(string(testLogPathType1) + ".pa")

	bs, err := ioutil.ReadFile(string(testLogPathType1) + ".gu")
	s.Require().NoError(err)
	s.Assert().Equal(100, bytes.Count(bs, []byte{'\n'}))
}

func (s *LogFilterOutputSuite) TestNoInput() {
	output := make(chan string, enoughLineBuffer)

	var st *outputState
	eg := &errgroup.Group{}
	eg.Go(func() error {
		var err error
		st, err = processOutput(s.log, output, testLogPathType1, nil, testMaxLines)
		return err
	})

	close(output)

	s.Assert().NoError(eg.Wait())

	s.Assert().Equal(0, st.Count)
	s.Assert().Equal(0, st.Rotation)

	s.Assert().FileExists(string(testLogPathType1) + ".gu")
	s.Assert().NoFileExists(string(testLogPathType1) + ".chi")
	s.Assert().NoFileExists(string(testLogPathType1) + ".pa")
}

func (s *LogFilterOutputSuite) TestInitialState() {
	output := make(chan string, enoughLineBuffer)

	var st *outputState
	eg := &errgroup.Group{}
	eg.Go(func() error {
		initState := &outputState{
			Rotation: 1,
			Count:    10,
		}
		var err error
		st, err = processOutput(s.log, output, testLogPathType1, initState, testMaxLines)
		return err
	})

	for i := 0; i < 110; i++ {
		output <- strconv.FormatInt(int64(i), 10)
	}

	close(output)

	s.Assert().NoError(eg.Wait())

	s.Assert().Equal(20, st.Count)
	s.Assert().Equal(2, st.Rotation)

	s.Assert().NoFileExists(string(testLogPathType1) + ".gu")
	s.Assert().FileExists(string(testLogPathType1) + ".chi")
	s.Assert().FileExists(string(testLogPathType1) + ".pa")
}
