package parser

import (
	"errors"
	"strconv"
	"time"
)

var (
	Verbs = []string{
		"get",
		"put",
		"add",
		"delete",
		"illuminate",
		"exit",
	}

	Objectives = []string{
		"vertex",
		"edge",
	}
	IlluminateObjectives = []string{
		"neighbor",
		"spt_relevance",
		"spt_cost",
		"mst_relevance",
		"mst_cost",
	}

	ErrNotFound = errors.New("not found")
	ErrNotEOF   = errors.New("not EOF")
)

func EOF(s *Source) error {
	if s.HasNext() {
		return ErrNotEOF
	}
	return nil
}

func String(s *Source) (string, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return "", err
	}

	return str, nil
}

func Integer(s *Source) (int, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(str)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func Float(s *Source) (float64, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func Bool(s *Source) (bool, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return false, err
	}
	v, err := strconv.ParseBool(str)
	if err != nil {
		return false, err
	}
	return v, nil
}

func Datetime(s *Source) (time.Time, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return time.Time{}, err
	}
	v, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return time.Time{}, err
	}
	return v, nil
}

func Value(s *Source) (interface{}, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return struct{}{}, err
	}
	if v, err := strconv.Atoi(str); err == nil {
		return v, nil
	}
	if v, err := strconv.ParseFloat(str, 64); err == nil {
		return v, nil
	}
	if v, err := strconv.ParseBool(str); err == nil {
		return v, nil
	}
	if v, err := time.Parse(time.RFC3339, str); err == nil {
		return v, nil
	}
	return str, nil
}

func Duration(s *Source) (time.Duration, error) {
	defer s.Next()
	i, err := Integer(s)
	if err != nil {
		return 0, err
	}
	return time.Duration(i) * time.Second, nil
}

func Float32(s *Source) (float32, error) {
	v, err := Float(s)
	if err != nil {
		return 0, err
	}
	return float32(v), nil
}

func AnyOf(s *Source, choices []string) (string, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return "", err
	}
	for _, v := range choices {
		if str == v {
			return str, nil
		}
	}
	return "", ErrNotFound
}

func Verb(s *Source) (string, error) {
	return AnyOf(s, Verbs)
}

func Objective(s *Source) (string, error) {
	return AnyOf(s, Objectives)
}

func IlluminateObjective(s *Source) (string, error) {
	return AnyOf(s, IlluminateObjectives)
}

func GetVertexParam(s *Source) (*GetVertex, error) {
	var err error
	m := &GetVertex{}
	if m.Key, err = String(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

func GetEdgeParam(s *Source) (*GetEdge, error) {
	var err error
	m := &GetEdge{}
	if m.Tail, err = String(s); err != nil {
		return nil, err
	}
	if m.Head, err = String(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

func PutVertexParam(s *Source) (*PutVertex, error) {
	var err error
	m := &PutVertex{}
	if m.Key, err = String(s); err != nil {
		return nil, err
	}
	if m.Value, err = Value(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err == nil {
		m.TTL = 24 * 365 * time.Hour
		return m, nil
	}
	if m.TTL, err = Duration(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}

	return m, nil
}

func PutEdgeParam(s *Source) (*PutEdge, error) {
	var err error
	m := &PutEdge{}
	if m.Tail, err = String(s); err != nil {
		return nil, err
	}
	if m.Head, err = String(s); err != nil {
		return nil, err
	}
	if m.Weight, err = Float32(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err == nil {
		m.TTL = 24 * 365 * time.Hour
		return m, nil
	}
	if m.TTL, err = Duration(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

func AddEdgeParam(s *Source) (*AddEdge, error) {
	var err error
	m := &AddEdge{}
	if m.Tail, err = String(s); err != nil {
		return nil, err
	}
	if m.Head, err = String(s); err != nil {
		return nil, err
	}
	if m.Weight, err = Float32(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err == nil {
		m.TTL = 24 * 365 * time.Hour
		return m, nil
	}
	if m.TTL, err = Duration(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}
func DeleteVertexParam(s *Source) (*DeleteVertex, error) {
	var err error
	m := &DeleteVertex{}
	if m.Key, err = String(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

func DeleteEdgeParam(s *Source) (*DeleteEdge, error) {
	var err error
	m := &DeleteEdge{}
	if m.Tail, err = String(s); err != nil {
		return nil, err
	}
	if m.Head, err = String(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

func IlluminateParam(s *Source) (*Illuminate, error) {
	var err error
	m := &Illuminate{}
	if m.Seed, err = String(s); err != nil {
		return nil, err
	}
	if m.Step, err = Integer(s); err != nil {
		return nil, err
	}
	if m.K, err = Integer(s); err != nil {
		return nil, err
	}
	if m.Tfidf, err = Bool(s); err != nil {
		return nil, err
	}

	return m, nil
}
