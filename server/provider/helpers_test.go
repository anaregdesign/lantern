package provider

import (
	"os"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func testEnvironmentReader() *envconfig.Reader {
	return envconfig.NewReader(os.Environ())
}
