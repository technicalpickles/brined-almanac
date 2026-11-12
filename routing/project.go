// Package routing decides whether a filing target is a GitHub repository or a taskwarrior
// project. It is the Go port of src/routing.rs. The Router that sends filings to a tracker waits
// for ivy/almanac's Go effect package.
package routing

import (
	"fmt"
	"strings"
)

// TaskPrefix starts a target that names a taskwarrior project.
const TaskPrefix = "task:"

// maxProject is the longest project name: the target slug is at most 200 bytes.
const maxProject = 200 - len(TaskPrefix)

// Project is the name of a taskwarrior project: dotted segments of [a-z0-9_-].
type Project struct{ name string }

// ParseProject validates a project name.
func ParseProject(s string) (Project, error) {
	if s == "" {
		return Project{}, fmt.Errorf("project name is empty")
	}
	if len(s) > maxProject {
		return Project{}, fmt.Errorf("project name is longer than %d bytes", maxProject)
	}
	for _, segment := range strings.Split(s, ".") {
		if segment == "" {
			return Project{}, fmt.Errorf("project %q has an empty dotted segment", s)
		}
		for i := 0; i < len(segment); i++ {
			b := segment[i]
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
				return Project{}, fmt.Errorf("project %q has a segment outside [a-z0-9_-]: %q", s, segment)
			}
		}
	}
	return Project{name: s}, nil
}

// String is the project name.
func (p Project) String() string { return p.name }

// Target is the task:<project> target for this project.
func (p Project) Target() string { return TaskPrefix + p.name }

// FromTarget reads a target. A target without the task: prefix is not a task target, which is
// reported as isTask false and no error. A target with the prefix and a bad project is an error.
func FromTarget(target string) (p Project, isTask bool, err error) {
	rest, ok := strings.CutPrefix(target, TaskPrefix)
	if !ok {
		return Project{}, false, nil
	}
	p, err = ParseProject(rest)
	return p, true, err
}
