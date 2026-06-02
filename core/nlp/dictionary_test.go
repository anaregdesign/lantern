package nlp

import (
	"reflect"
	"testing"
)

func TestDictionary_AddWord(t *testing.T) {
	type fields struct {
		ID2Word map[int]string
		Word2ID map[string]int
	}
	type args struct {
		word string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "TestDictionary_AddWord",
			fields: fields{
				ID2Word: map[int]string{
					0: "zero",
				},
				Word2ID: map[string]int{
					"zero": 0,
				},
			},
			args: args{
				word: "one",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Dictionary{
				ID2Word: tt.fields.ID2Word,
				Word2ID: tt.fields.Word2ID,
			}
			d.AddWord(tt.args.word)
		})
	}
}

func TestDictionary_AddWords(t *testing.T) {
	type fields struct {
		ID2Word map[int]string
		Word2ID map[string]int
	}
	type args struct {
		words []string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "TestDictionary_AddWords",
			fields: fields{
				ID2Word: map[int]string{
					0: "zero",
				},
				Word2ID: map[string]int{
					"zero": 0,
				},
			},
			args: args{
				words: []string{"one", "two", "three"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Dictionary{
				ID2Word: tt.fields.ID2Word,
				Word2ID: tt.fields.Word2ID,
			}
			d.AddWords(tt.args.words)
		})
	}
}

func TestDictionary_Words2BOW(t *testing.T) {
	type fields struct {
		ID2Word map[int]string
		Word2ID map[string]int
	}
	type args struct {
		words []string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   BOW
	}{
		{
			name: "TestDictionary_Words2BOW",
			fields: fields{
				ID2Word: map[int]string{
					0: "zero",
					1: "one",
				},
				Word2ID: map[string]int{
					"zero": 0,
					"one":  1,
				},
			},
			args: args{
				words: []string{"one", "two", "three"},
			},
			want: BOW{
				1: 1,
				2: 1,
				3: 1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Dictionary{
				ID2Word: tt.fields.ID2Word,
				Word2ID: tt.fields.Word2ID,
			}
			if got := d.Words2BOW(tt.args.words); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Words2BOW() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDictionary_Words2CBOW(t *testing.T) {
	type fields struct {
		ID2Word map[int]string
		Word2ID map[string]int
	}
	type args struct {
		words  []string
		window int
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   CBOW
	}{
		{
			name: "TestDictionary_Words2CBOW",
			fields: fields{
				ID2Word: map[int]string{},
				Word2ID: map[string]int{},
			},
			args: args{
				words:  []string{"one", "two", "three"},
				window: 1,
			},
			want: CBOW{
				{
					Source: 0,
					Bow: BOW{
						1: 1,
					},
				},
				{
					Source: 1,
					Bow: BOW{
						0: 1,
						2: 1,
					},
				},
				{
					Source: 2,
					Bow: BOW{
						1: 1,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Dictionary{
				ID2Word: tt.fields.ID2Word,
				Word2ID: tt.fields.Word2ID,
			}
			if got := d.Words2CBOW(tt.args.words, tt.args.window); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Words2CBOW() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewBOW(t *testing.T) {
	tests := []struct {
		name string
		want BOW
	}{
		{
			name: "TestNewBOW",
			want: BOW{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewBOW(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewBOW() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewDictionary(t *testing.T) {
	tests := []struct {
		name string
		want *Dictionary
	}{
		{
			name: "TestNewDictionary",
			want: &Dictionary{
				ID2Word: map[int]string{},
				Word2ID: map[string]int{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewDictionary(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewDictionary() = %v, want %v", got, tt.want)
			}
		})
	}
}
