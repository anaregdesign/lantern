package parser

import (
	"errors"
	"strings"
)

var (
	ErrOutOfIndex = errors.New("out of index")
)

type Source struct {
	s      []string
	pos    int
	length int
}

func NewSource(str string) *Source {
	var ss string
	ss = strings.TrimSpace(str)
	s := strings.Split(ss, " ")
	return &Source{
		s:      s,
		pos:    0,
		length: len(s),
	}
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
