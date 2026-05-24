package github

import (
	"fmt"
	"strings"
)

// buildWebURL builds the GitHub web URL for a file in a repository.
// Example: https://github.com/owner/repo/blob/main/docs/features/x/product-spec.md
func buildWebURL(owner, repo, ref, filePath string) string {
	// Strip leading slash if present.
	filePath = strings.TrimPrefix(filePath, "/")
	return fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s", owner, repo, ref, filePath)
}
