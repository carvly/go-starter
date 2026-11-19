package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveFromSpec_KeepsReferencedSchemas(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "admin.yaml")
	content := `paths:
  /users:
    get:
      operationId: listUsers
      tags: [users]
      parameters:
        - name: status
          in: query
          schema:
            $ref: "#/components/schemas/UserStatus"
  /products:
    get:
      operationId: listProducts
      tags: [products]
components:
  schemas:
    UserStatus:
      type: string
    ProductList:
      type: object
`
	if err := os.WriteFile(spec, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := removeFromSpec(content, "products")
	if !strings.Contains(got, "UserStatus:") {
		t.Error("保留的 UserStatus 被误删")
	}
	if strings.Contains(got, "ProductList:") {
		t.Error("未被引用的 ProductList 没有删除")
	}
}
