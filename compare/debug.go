package compare

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// debug is one value in Rust's {:?} output: a string, a number or a bare word, a struct
// `Name { field: value }`, a tuple `Name(value)`, or a list `[value]`. The Rust simulate
// prints each outcome this way, so this is how its effects are read.
type debug struct {
	// name is a struct's or tuple's name, a bare word such as WouldApply, or a number.
	name   string
	text   string
	quoted bool
	fields map[string]debug
	items  []debug
}

// str is the value as text: a quoted string, a one-item tuple holding one such as
// Title("..."), or a bare word.
func (d debug) str() string {
	switch {
	case d.quoted:
		return d.text
	case len(d.items) == 1 && d.fields == nil:
		return d.items[0].str()
	}
	return d.name
}

// parseDebug reads one value from the start of text, returning what is left after it.
func parseDebug(text string) (debug, string, error) {
	p := &debugParser{text: text}
	d, err := p.value()
	if err != nil {
		return debug{}, "", fmt.Errorf("reading Rust's output %q at %d: %w", text, p.at, err)
	}
	return d, p.text[p.at:], nil
}

type debugParser struct {
	text string
	at   int
}

func (p *debugParser) skipSpace() {
	for p.at < len(p.text) && p.text[p.at] == ' ' {
		p.at++
	}
}

func (p *debugParser) peek() byte {
	p.skipSpace()
	if p.at >= len(p.text) {
		return 0
	}
	return p.text[p.at]
}

func (p *debugParser) expect(c byte) error {
	if p.peek() != c {
		return fmt.Errorf("expected %q", c)
	}
	p.at++
	return nil
}

func (p *debugParser) value() (debug, error) {
	switch c := p.peek(); {
	case c == '"':
		text, err := p.quoted()
		return debug{text: text, quoted: true}, err
	case c == '[':
		p.at++
		items, err := p.list(']')
		return debug{items: items}, err
	case c == '.' && strings.HasPrefix(p.text[p.at:], ".."):
		p.at += 2
		return debug{name: ".."}, nil
	case c == '-' || c == '_' || c < unicode.MaxASCII && (unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))):
		return p.named()
	}
	return debug{}, fmt.Errorf("unexpected %q", p.peek())
}

func (p *debugParser) word() string {
	start := p.at
	for p.at < len(p.text) {
		c := rune(p.text[p.at])
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' && c != '.' {
			break
		}
		p.at++
	}
	return p.text[start:p.at]
}

func (p *debugParser) named() (debug, error) {
	d := debug{name: p.word()}
	switch p.peek() {
	case '(':
		p.at++
		items, err := p.list(')')
		d.items = items
		return d, err
	case '{':
		p.at++
		fields, err := p.fields()
		d.fields = fields
		return d, err
	}
	return d, nil
}

func (p *debugParser) list(end byte) ([]debug, error) {
	var out []debug
	for p.peek() != end {
		if p.peek() == 0 {
			return nil, fmt.Errorf("expected %q", end)
		}
		item, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
		if p.peek() == ',' {
			p.at++
		}
	}
	p.at++
	return out, nil
}

func (p *debugParser) fields() (map[string]debug, error) {
	out := map[string]debug{}
	for p.peek() != '}' {
		if p.peek() == 0 {
			return nil, fmt.Errorf("expected '}'")
		}
		p.skipSpace()
		name := p.word()
		if name == "" {
			return nil, fmt.Errorf("expected a field name")
		}
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		out[name] = value
		if p.peek() == ',' {
			p.at++
		}
	}
	p.at++
	return out, nil
}

// quoted reads a Rust string literal as {:?} writes it, escapes and all.
func (p *debugParser) quoted() (string, error) {
	p.at++
	var b strings.Builder
	for p.at < len(p.text) {
		c := p.text[p.at]
		p.at++
		switch c {
		case '"':
			return b.String(), nil
		case '\\':
			if p.at >= len(p.text) {
				return "", fmt.Errorf("unfinished escape")
			}
			e := p.text[p.at]
			p.at++
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '0':
				b.WriteByte(0)
			case '\\', '"', '\'':
				b.WriteByte(e)
			case 'u':
				end := strings.IndexByte(p.text[p.at:], '}')
				if !strings.HasPrefix(p.text[p.at:], "{") || end < 0 {
					return "", fmt.Errorf("bad \\u escape")
				}
				code, err := strconv.ParseUint(p.text[p.at+1:p.at+end], 16, 32)
				if err != nil {
					return "", fmt.Errorf("bad \\u escape: %w", err)
				}
				b.WriteRune(rune(code))
				p.at += end + 1
			default:
				return "", fmt.Errorf("unknown escape \\%c", e)
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", fmt.Errorf("unfinished string")
}

// kebab is a Rust variant's name as Ivy's Go library writes the same value: WouldApply is
// would-apply, and Queued is queued.
func kebab(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
