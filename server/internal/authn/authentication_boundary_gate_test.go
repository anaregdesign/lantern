package authn

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Covers the production dependency DAG and both concrete producer locations.
// Black-box tests may import the real composition without creating a back edge.
func TestAuthenticationPackageBoundaries(t *testing.T) {
	const server = "github.com/anaregdesign/lantern/server/"
	forbidden := map[string][]string{
		"internal/authn":             {"net/http", "connectrpc.com/", "github.com/anaregdesign/lantern/pb/", server + "provider", server + "service", server + "internal/securitytransport", server + "internal/rpcwire"},
		"internal/oidc":              {server + "internal/authn", server + "provider", server + "service", server + "internal/securitytransport", server + "internal/rpcwire"},
		"internal/security":          {server + "internal/oidc", server + "internal/authn", server + "provider", server + "service", server + "internal/securitytransport", server + "internal/rpcwire"},
		"internal/securitytransport": {server + "provider", server + "service"},
		"internal/rpcwire":           {server + "provider", server + "service", server + "internal/securitytransport", server + "internal/authn", server + "internal/oidc", server + "internal/security"},
		"service":                    {server + "provider", server + "internal/securitytransport"},
		"provider":                   {},
	}
	var producers []string
	for pkg, imports := range forbidden {
		dir := filepath.Join("../..", pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, imported := range file.Imports {
				path, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				for _, blocked := range imports {
					if path == blocked || strings.HasPrefix(path, strings.TrimSuffix(blocked, "/")+"/") {
						t.Errorf("%s/%s imports forbidden %s", pkg, entry.Name(), path)
					}
				}
			}
			for _, declaration := range file.Decls {
				if method, ok := declaration.(*ast.FuncDecl); ok && method.Recv != nil && method.Name.Name == "VerifyCurrentCredential" {
					producers = append(producers, pkg+"/"+entry.Name())
				}
			}
		}
	}
	slices.Sort(producers)
	want := []string{"internal/authn/current_credential.go", "internal/securitytransport/credential.go"}
	if !slices.Equal(producers, want) {
		t.Fatalf("production credential producer boundary changed: %v", producers)
	}
}
