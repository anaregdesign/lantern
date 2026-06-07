package parser

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrOutOfIndex = errors.New("out of index")
	// ErrUnterminatedString is returned by NewSource when the input
	// contains an unmatched single- or double-quote opener. See #438.
	ErrUnterminatedString = errors.New("unterminated string literal")
)

type Source struct {
	s      []string
	pos    int
	length int
}

// NewSource tokenises the input into a Source. The grammar (#438):
//
//	token         = bareword | double-quoted | single-quoted
//	bareword      = first char ∉ {whitespace,'"',"'"}, continues until next whitespace.
//	                Quotes inside a bareword are kept verbatim.
//	double-quoted = "..." with C-style escapes \", \\, \n, \r, \t.
//	single-quoted = '...' verbatim (no escapes).
//
// Quoting is only special at the start of a token. Unterminated quotes
// (or unknown backslash sequences inside a double-quoted token) return
// ErrUnterminatedString or a parse error. On any error the returned
// Source is still valid (empty token stream) so callers that ignore
// the error do not deref a nil pointer.
func NewSource(str string) (*Source, error) {
	tokens, err := tokenise(str)
	if err != nil {
		return &Source{s: nil, pos: 0, length: 0}, err
	}
	return &Source{
		s:      tokens,
		pos:    0,
		length: len(tokens),
	}, nil
}

func tokenise(input string) ([]string, error) {
	var out []string
	i := 0
	n := len(input)
	for i < n {
		ch := input[i]
		if isSpace(ch) {
			i++
			continue
		}
		switch ch {
		case '"':
			value, next, err := scanDoubleQuoted(input, i)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
			i = next
		case '\'':
			value, next, err := scanSingleQuoted(input, i)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
			i = next
		default:
			// bareword: consume to next whitespace; quotes mid-token are
			// part of the bareword.
			j := i + 1
			for j < n && !isSpace(input[j]) {
				j++
			}
			out = append(out, input[i:j])
			i = j
		}
	}
	return out, nil
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

func scanDoubleQuoted(input string, start int) (string, int, error) {
	// input[start] == '"'
	var sb strings.Builder
	i := start + 1
	n := len(input)
	for i < n {
		ch := input[i]
		if ch == '"' {
			return sb.String(), i + 1, nil
		}
		if ch == '\\' {
			if i+1 >= n {
				return "", 0, ErrUnterminatedString
			}
			esc := input[i+1]
			switch esc {
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			default:
				return "", 0, fmt.Errorf("invalid escape sequence \\%c", esc)
			}
			i += 2
			continue
		}
		sb.WriteByte(ch)
		i++
	}
	return "", 0, ErrUnterminatedString
}

func scanSingleQuoted(input string, start int) (string, int, error) {
	// input[start] == '\''
	i := start + 1
	n := len(input)
	for i < n {
		if input[i] == '\'' {
			return input[start+1 : i], i + 1, nil
		}
		i++
	}
	return "", 0, ErrUnterminatedString
}

func (s *Source) Next() {
	s.pos++
}

func (s *Source) Peek() (string, error) {
	if s.pos >= s.length {
		return "", ErrOutOfIndex
	}
	return s.s[s.pos], nil
}

func (s *Source) HasNext() bool {
	return s.pos < s.length
}

func (s *Source) Reset() {
	s.pos = 0
}

func (s *Source) Len() int {
	return s.length
}

func (s *Source) Pos() int {
	return s.pos
}

func (s *Source) SetPos(pos int) {
	s.pos = pos
}

func (s *Source) Slice() []string {
	return s.s
}
