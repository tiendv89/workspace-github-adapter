package urlutil

import (
	"testing"
)

func TestParseGitHubRepo_Valid(t *testing.T) {
	cases := []struct {
		raw       string
		wantOwner string
		wantRepo  string
	}{
		{"https://github.com/acme/repo", "acme", "repo"},
		{"https://github.com/acme/repo.git", "acme", "repo"},
		{"https://github.com/acme/my-repo", "acme", "my-repo"},
	}
	for _, tc := range cases {
		owner, repo, err := ParseGitHubRepo(tc.raw)
		if err != nil {
			t.Errorf("ParseGitHubRepo(%q): unexpected error: %v", tc.raw, err)
			continue
		}
		if owner != tc.wantOwner || repo != tc.wantRepo {
			t.Errorf("ParseGitHubRepo(%q) = (%q, %q), want (%q, %q)", tc.raw, owner, repo, tc.wantOwner, tc.wantRepo)
		}
	}
}

func TestParseGitHubRepo_Invalid(t *testing.T) {
	cases := []string{
		"not-a-url",
		"https://gitlab.com/acme/repo",
		"https://github.com/",
		"https://github.com/acme",
	}
	for _, raw := range cases {
		_, _, err := ParseGitHubRepo(raw)
		if err == nil {
			t.Errorf("ParseGitHubRepo(%q): expected error, got nil", raw)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"my repo", "my-repo"},
		{"acme/repo", "acme-repo"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"", ""},
	}
	for _, tc := range cases {
		got := Slugify(tc.input)
		if got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
