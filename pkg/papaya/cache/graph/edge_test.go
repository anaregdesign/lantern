package graph

import (
	"reflect"
	"testing"
	"time"
)

func Test_newWeight(t *testing.T) {
	tests := []struct {
		name string
		want *weight
	}{
		{
			name: "newWeight",
			want: &weight{
				values: make([]weightValue, 0),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newWeight(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("newWeight() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_weightValue_expired(t *testing.T) {
	type fields struct {
		value float32
		ttl   time.Time
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "weightValue_expired",
			fields: fields{
				value: 1,
				ttl:   time.Now().Add(-time.Minute),
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := weightValue{
				value:      tt.fields.value,
				expiration: tt.fields.ttl,
			}
			if got := w.expired(); got != tt.want {
				t.Errorf("expired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_weight_add(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	type args struct {
		value float32
		ttl   time.Duration
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "weight_add",
			fields: fields{
				values: make([]weightValue, 0),
			},
			args: args{
				value: 1,
				ttl:   time.Minute,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			w.addWithTTL(tt.args.value, tt.args.ttl)
		})
	}
}

func Test_weight_value(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	tests := []struct {
		name   string
		fields fields
		want   float32
	}{
		{
			name: "weight_Value",
			fields: fields{
				values: []weightValue{
					{
						value:      1,
						expiration: time.Now().Add(time.Minute),
					},
					{
						value:      1,
						expiration: time.Now().Add(-time.Minute),
					},
				},
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			if got := w.value(); got != tt.want {
				t.Errorf("value() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_weight_isZero(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "weight_isZero",
			fields: fields{
				values: []weightValue{
					{
						value:      1,
						expiration: time.Now().Add(time.Minute),
					},
				},
			},
			want: false,
		},
		{
			name: "weight_isZero",
			fields: fields{
				values: []weightValue{
					{
						value:      1,
						expiration: time.Now().Add(-time.Minute),
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			if got := w.isZero(); got != tt.want {
				t.Errorf("isZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_edgeCache_delete(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
	}
	type testCase[S comparable] struct {
		name string
		c    edgeCache[S]
		args args[S]
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_delete",
			c: edgeCache[string]{
				tf: make(map[string]map[string]*weight),
			},
			args: args[string]{
				tail: "a",
				head: "b",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.delete(tt.args.tail, tt.args.head)
		})
	}
}

func Test_edgeCache_get(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
	}
	type testCase[S comparable] struct {
		name  string
		c     edgeCache[S]
		args  args[S]
		want  float32
		want1 bool
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_get",
			c: edgeCache[string]{
				tf: make(map[string]map[string]*weight),
			},
			args: args[string]{
				tail: "a",
				head: "b",
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := tt.c.get(tt.args.tail, tt.args.head)
			if got != tt.want {
				t.Errorf("get() = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("get() = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func Test_edgeCache_set(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
		w    float32
	}
	type testCase[S comparable] struct {
		name string
		c    edgeCache[S]
		args args[S]
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_set",
			c: edgeCache[string]{
				tf: make(map[string]map[string]*weight),
				df: make(map[string]int),
			},
			args: args[string]{
				tail: "a",
				head: "b",
				w:    1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.add(tt.args.tail, tt.args.head, tt.args.w)
		})
	}
}

func Test_edgeCache_setWithExpiration(t *testing.T) {
	type args[S comparable] struct {
		tail       S
		head       S
		w          float32
		expiration time.Time
	}
	type testCase[S comparable] struct {
		name string
		c    edgeCache[S]
		args args[S]
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_setWithExpiration",
			c: edgeCache[string]{
				tf: make(map[string]map[string]*weight),
				df: make(map[string]int),
			},
			args: args[string]{
				tail: "a",
				head: "b",
				w:    1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.addWithExpiration(tt.args.tail, tt.args.head, tt.args.w, tt.args.expiration)
		})
	}
}

func Test_edgeCache_setWithTTL(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
		w    float32
		ttl  time.Duration
	}
	type testCase[S comparable] struct {
		name string
		c    edgeCache[S]
		args args[S]
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_setWithTTL",
			c: edgeCache[string]{
				tf: make(map[string]map[string]*weight),
				df: make(map[string]int),
			},
			args: args[string]{
				tail: "a",
				head: "b",
				w:    1,
				ttl:  time.Second,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.addWithTTL(tt.args.tail, tt.args.head, tt.args.w, tt.args.ttl)
		})
	}
}

func Test_weight_addWithExpiration(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	type args struct {
		value      float32
		expiration time.Time
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "weight_addWithExpiration",
			fields: fields{
				values: []weightValue{},
			},
			args: args{
				value:      1,
				expiration: time.Now().Add(time.Second),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			w.addWithExpiration(tt.args.value, tt.args.expiration)
		})
	}
}

func Test_weight_addWithTTL(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	type args struct {
		value float32
		ttl   time.Duration
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "weight_addWithTTL",
			fields: fields{
				values: []weightValue{},
			},
			args: args{
				value: 1,
				ttl:   time.Second,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			w.addWithTTL(tt.args.value, tt.args.ttl)
		})
	}
}
