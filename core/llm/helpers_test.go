package llm

// Shared struct fixtures for the llm package tests.

type person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type address struct {
	City string `json:"city"`
	Zip  string `json:"zip,omitempty"`
}

type profile struct {
	Person  person            `json:"person"`
	Emails  []string          `json:"emails"`
	Address *address          `json:"address"`
	Tags    map[string]string `json:"tags"`
	Ignored string            `json:"-"`
	note    string
}
