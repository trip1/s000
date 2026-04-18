package compatibility_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatibilityArtifactsExist(t *testing.T) {
	t.Parallel()

	root := projectRoot(t)
	required := []string{
		"docs/compatibility-matrix.md",
		"scripts/awscli-e2e.sh",
		"scripts/sdk-smoke-go.sh",
		"scripts/sdk-smoke-python.sh",
		"scripts/sdk-smoke-js.sh",
	}
	for _, rel := range required {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
				t.Fatalf("expected artifact %s: %v", rel, err)
			}
		})
	}
}

func TestCompatibilityMatrixCoversRelease13Items(t *testing.T) {
	t.Parallel()

	root := projectRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs/compatibility-matrix.md"))
	if err != nil {
		t.Fatalf("read matrix failed: %v", err)
	}
	text := strings.ToLower(string(body))
	for _, needle := range []string{"listbuckets", "putobject", "getobject", "multipart", "listobjectsv2", "copyobject", "deleteobject"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected compatibility matrix to include %q", needle)
		}
	}
}

func TestAwsCliScriptIncludesCoreFlows(t *testing.T) {
	t.Parallel()

	root := projectRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "scripts/awscli-e2e.sh"))
	if err != nil {
		t.Fatalf("read aws-cli script failed: %v", err)
	}
	text := strings.ToLower(string(body))
	for _, needle := range []string{"s3 ls", "s3 cp", "s3 sync", "create-multipart-upload", "upload-part", "complete-multipart-upload"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected aws-cli script to include %q", needle)
		}
	}
}

func TestSdkSmokeScriptsDeclareExpectedClientCalls(t *testing.T) {
	t.Parallel()

	root := projectRoot(t)
	checks := map[string][]string{
		"scripts/sdk-smoke-go.sh":     {"createbucket", "putobject", "getobject", "deleteobject"},
		"scripts/sdk-smoke-python.sh": {"create_bucket", "put_object", "get_object", "delete_object"},
		"scripts/sdk-smoke-js.sh":     {"createbucketcommand", "putobjectcommand", "getobjectcommand", "deleteobjectcommand"},
	}

	for rel, needles := range checks {
		rel, needles := rel, needles
		t.Run(rel, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatalf("read script failed: %v", err)
			}
			text := strings.ToLower(string(body))
			for _, needle := range needles {
				if !strings.Contains(text, needle) {
					t.Fatalf("expected %s to include %q", rel, needle)
				}
			}
		})
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("failed to locate project root from %s: %v", wd, err)
	}
	return root
}
