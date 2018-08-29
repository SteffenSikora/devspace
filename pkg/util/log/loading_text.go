package log

import (
	"io"
	"time"

	"github.com/daviddengcn/go-colortext"
)

const waitInterval = time.Millisecond * 150

type loadingText struct {
	Stream  io.Writer
	Message string

	loadingRune int
	isShown     bool
	stopChan    chan bool
}

func (l *loadingText) Start() {
	l.isShown = false

	if l.stopChan == nil {
		l.stopChan = make(chan bool)
	}

	go func() {
		l.render()

		for {
			select {
			case <-l.stopChan:
				return
			case <-time.After(waitInterval):
				l.render()
			}
		}
	}()
}

func (l *loadingText) getLoadingChar() string {
	var loadingChar string

	switch l.loadingRune {
	case 0:
		loadingChar = "|"
	case 1:
		loadingChar = "/"
	case 2:
		loadingChar = "-"
	case 3:
		loadingChar = "\\"
	}

	l.loadingRune++

	if l.loadingRune > 3 {
		l.loadingRune = 0
	}

	return loadingChar
}

func (l *loadingText) render() {
	if l.isShown == false {
		l.isShown = true
	} else {
		l.Stream.Write([]byte("\r"))
	}

	ct.Foreground(ct.Red, false)
	l.Stream.Write([]byte("[WAIT] "))
	ct.ResetColor()

	l.Stream.Write([]byte(l.getLoadingChar() + " " + l.Message))
}

func (l *loadingText) Stop() {
	l.stopChan <- true
	l.Stream.Write([]byte("\r"))

	messageLength := len(l.Message) + 9

	for i := 0; i < messageLength; i++ {
		l.Stream.Write([]byte(" "))
	}

	l.Stream.Write([]byte("\r"))
}
