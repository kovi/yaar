package main

import (
	"os/exec"
	"strings"

	log "github.com/sirupsen/logrus"
)

type Command struct {
	filename string
	command  string
}

var triggers chan string
var commands chan Command

func StartTriggers() chan bool {
	triggers = make(chan string)
	commands = make(chan Command)
	quit := make(chan bool)
	go func() {
		for {
			select {
			case <-quit:
				return
			case f := <-triggers:
				onTrigger(f)
			}
		}
	}()

	go func() {
		for {
			select {
			case <-quit:
				return
			case cmd := <-commands:
				log.Info("Executing cmd '", cmd.command, "' on ", cmd.filename)
				r := exec.Command("/bin/sh", "-c", cmd.command)
				o, _ := r.Output()
				log.Info("cmd exited=", r.ProcessState.ExitCode(), " out=", string(o))
			}
		}
	}()

	return quit
}

type Tag struct {
	name, value string
}

func NewTag(s string) Tag {
	ts := strings.SplitN(s, "=", 2)
	if len(ts) == 1 {
		return Tag{s, ""}
	}
	return Tag{ts[0], ts[1]}
}

/* matches checks whether every condition in trigger is ok for file */
func (t Trigger) matches(filename string, m Metadata) bool {
	if len(t.File) > 0 && !strings.HasPrefix(filename, t.File) {
		return false
	}

	if len(t.Tag) > 0 {
		ttag := NewTag(t.Tag)
		hasMatchingTag := false
		for _, mt := range m.Tags {
			mtag := NewTag(mt)
			// no condition, just check tag name
			if len(ttag.value) == 0 && ttag.name == mtag.name {
				hasMatchingTag = true
				break
			}

			if ttag.value == mtag.value {
				hasMatchingTag = true
				break
			}
		}

		// no tag matching
		if !hasMatchingTag {
			return false
		}
	}

	return true
}

func onTrigger(filename string) {
	log.Info("Trigger for ", filename, "-", Config.Triggers)

	m, _ := GetMetadata(filename)
	for _, v := range Config.Triggers {
		if !v.matches(filename, m) {
			continue
		}

		if len(v.Execute) != 0 {
			commands <- Command{filename, v.Execute}
		}
	}
}
