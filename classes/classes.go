// Package classes maps an error's text to a failure class. It is the Go port of src/classes.rs
// and reads the same classes.toml, so a row added there changes both implementations.
package classes

import (
	"fmt"
	"regexp"

	"github.com/BurntSushi/toml"

	brined "github.com/technicalpickles/brined-almanac"
)

// Class is one row of classes.toml.
type Class struct {
	Name string
	// Threshold is the events an issue needs before it files. Nil never files.
	Threshold *uint64
	Why       string

	pattern *regexp.Regexp
}

// HasPattern reports whether the class matches by pattern. Only the last row, other, does not.
func (c Class) HasPattern() bool { return c.pattern != nil }

type row struct {
	Name      string  `toml:"name"`
	Pattern   *string `toml:"pattern"`
	Threshold *uint64 `toml:"threshold"`
	Why       string  `toml:"why"`
}

type table struct {
	Class []row `toml:"class"`
}

// Table is the ordered class table. The first row whose pattern matches wins, and other is last.
type Table struct {
	rows []Class
}

// Parse reads a classes.toml. It fails on a bad pattern, an empty table, or an other row that is
// not last.
func Parse(src []byte) (*Table, error) {
	var raw table
	if err := toml.Unmarshal(src, &raw); err != nil {
		return nil, fmt.Errorf("classes.toml: %w", err)
	}
	if len(raw.Class) == 0 {
		return nil, fmt.Errorf("classes.toml: no rows")
	}
	rows := make([]Class, 0, len(raw.Class))
	for _, r := range raw.Class {
		c := Class{Name: r.Name, Threshold: r.Threshold, Why: r.Why}
		if r.Pattern != nil {
			re, err := regexp.Compile(*r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("classes.toml: %s: %w", r.Name, err)
			}
			c.pattern = re
		}
		rows = append(rows, c)
	}
	if rows[len(rows)-1].Name != "other" {
		return nil, fmt.Errorf("classes.toml: other must be the last row")
	}
	return &Table{rows: rows}, nil
}

// Default is the table embedded from classes.toml.
func Default() *Table {
	t, err := Parse(brined.ClassesTOML)
	if err != nil {
		panic(err)
	}
	return t
}

// Rows returns the classes in match order.
func (t *Table) Rows() []Class { return t.rows }

func (t *Table) other() Class { return t.rows[len(t.rows)-1] }

// Classify returns the first class whose pattern matches text, or other.
func (t *Table) Classify(text string) Class {
	for _, c := range t.rows {
		if c.pattern != nil && c.pattern.MatchString(text) {
			return c
		}
	}
	return t.other()
}

// Named returns the class with this name, or other when none has it.
func (t *Table) Named(name string) Class {
	for _, c := range t.rows {
		if c.Name == name {
			return c
		}
	}
	return t.other()
}

// OwnerOf returns the repository that files for a service: brineworks files to its own, every
// other service to pickleclaw.
func OwnerOf(service string) string {
	if service == "brineworks" {
		return "technicalpickles/brineworks"
	}
	return "technicalpickles/pickleclaw"
}
