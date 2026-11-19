package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnershipDriftDetectsPermissionChange(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"internal/modules/auth/adminapi/oapi/config.yaml": "include-tags: [users]\n",
		"internal/modules/auth/httpapi/oapi/config.yaml":  "include-tags: [auth]\n",
		"api/admin.yaml": `openapi: 3.2.0
paths:
  /users:
    get:
      operationId: listUsers
      tags: [users]
      x-permission: user:read
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/User"
components:
  schemas:
    User:
      type: object
`,
		"api/openapi.yaml": "openapi: 3.2.0\npaths: {}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-qm", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "api/admin.yaml"), []byte(strings.Replace(files["api/admin.yaml"], "user:read", "user:write", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	problems, err := ownershipDrift(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) == 0 {
		t.Fatal("expected permission drift")
	}
}
