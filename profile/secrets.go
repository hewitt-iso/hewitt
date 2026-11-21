package profile

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Host configuration is imported into profiles that agents read, and agents
// can send what they read. Unless the user chose to keep them, secret values
// in imported settings are removed and the setting names are reported, so
// MCP servers, providers, and shell settings arrive without credentials.
// Files that usually are credentials are never copied from directories.

// secretKey matches setting, header, flag, and variable names that usually
// hold credentials.
var secretKey = regexp.MustCompile(`(?i)(token|secret|passw|api[_-]?key|access[_-]?key|private[_-]?key|credential|cookie|authorization|bearer|extraheader|^auth$|_auth$)`)

// settingKey matches names that select how or where a credential is found,
// such as cli_auth_credentials_store, bearer_token_env_var, or apiKeyHelper;
// their values are not secrets.
var settingKey = regexp.MustCompile(`(?i)(store|method|mode|env_?var|helper|file|path|url|uri|endpoint|type)$`)

// secretValue matches well-known token formats wherever they appear.
var secretValue = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{10,}|sk-(?:proj-)?[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}`)

var (
	envName      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	urlUserinfo  = regexp.MustCompile(`([a-z][a-z0-9+.-]*://)[^/@\s:"']+:[^/@\s"']+@`)
	flagValue    = regexp.MustCompile(`(--[A-Za-z0-9_-]+)=([^\s"',]+)`)
	jsonPair     = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"(\s*:\s*)"((?:[^"\\]|\\.)*)"`)
	jsonFlagArg  = regexp.MustCompile(`"(--?[A-Za-z0-9_-]+)"(\s*,\s*)"((?:[^"\\]|\\.)*)"`)
	tomlPair     = regexp.MustCompile(`([A-Za-z0-9_-]+|"[^"]*")(\s*=\s*)("(?:[^"\\]|\\.)*"|'[^']*')`)
	iniPair      = regexp.MustCompile(`(?m)^(\s*)([A-Za-z0-9_.-]+)(\s*=\s*)(.*)$`)
	shellAssign  = regexp.MustCompile(`(?m)^(\s*(?:export\s+)?)([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	exampleFiles = []string{".env.example", ".env.sample", ".env.template"}
)

// SensitiveFile reports names that usually are credentials or private keys.
func SensitiveFile(name string) bool {
	lower := strings.ToLower(name)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") && !slices.Contains(exampleFiles, lower) {
		return true
	}
	switch lower {
	case "credentials", "credentials.json", ".credentials.json", "auth.json", ".netrc", ".git-credentials",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519":
		return true
	}
	switch filepath.Ext(lower) {
	case ".pem", ".key", ".p12", ".pfx", ".jks", ".keystore":
		return true
	}
	return false
}

// reference reports values that name a secret instead of containing it, such
// as ${GITHUB_TOKEN}, {env:API_KEY}, or an environment variable name.
func reference(value string) bool {
	value = strings.Trim(value, `"'`)
	return value == "" || strings.HasPrefix(value, "$") || strings.HasPrefix(value, "{env:") || envName.MatchString(value)
}

// redactor removes secret values from imported files unless keep is set, and
// records what it removed as "file: name".
type redactor struct {
	keep     bool
	home     string
	redacted []string
}

func (r *redactor) note(path, what string) {
	if rel, err := filepath.Rel(r.home, path); err == nil && filepath.IsLocal(rel) {
		path = "~/" + filepath.ToSlash(rel)
	}
	entry := path + ": " + what
	if !slices.Contains(r.redacted, entry) {
		r.redacted = append(r.redacted, entry)
	}
}

// filter returns the contents to write for the host file src, or skip for a
// credential file in an imported directory.
func (r *redactor) filter(src string, data []byte, walked bool) (out []byte, skip bool) {
	if walked && SensitiveFile(filepath.Base(src)) {
		r.note(src, "file not copied")
		return nil, true
	}
	if r.keep || bytes.IndexByte(data, 0) >= 0 {
		return data, false
	}
	return []byte(r.redact(src, string(data))), false
}

// redact removes secrets from text according to the file's format, then
// removes known token formats and URL credentials in any file.
func (r *redactor) redact(src, text string) string {
	base := strings.ToLower(filepath.Base(src))
	name := func(key string) bool {
		key = strings.Trim(key, `"`)
		return secretKey.MatchString(key) && !settingKey.MatchString(key)
	}
	switch {
	case strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".jsonc"):
		text = jsonPair.ReplaceAllStringFunc(text, func(m string) string {
			p := jsonPair.FindStringSubmatch(m)
			if !name(p[1]) || reference(p[3]) {
				return m
			}
			r.note(src, p[1])
			return `"` + p[1] + `"` + p[2] + `""`
		})
		text = jsonFlagArg.ReplaceAllStringFunc(text, func(m string) string {
			p := jsonFlagArg.FindStringSubmatch(m)
			if !name(p[1]) || reference(p[3]) || strings.HasPrefix(p[3], "-") {
				return m
			}
			r.note(src, p[1])
			return `"` + p[1] + `"` + p[2] + `""`
		})
	case strings.HasSuffix(base, ".toml"):
		text = tomlPair.ReplaceAllStringFunc(text, func(m string) string {
			p := tomlPair.FindStringSubmatch(m)
			if !name(p[1]) || reference(p[3]) {
				return m
			}
			r.note(src, strings.Trim(p[1], `"`))
			return p[1] + p[2] + `""`
		})
	case base == ".gitconfig" || base == ".npmrc" || strings.Contains(filepath.ToSlash(src), "/.config/git/"):
		text = iniPair.ReplaceAllStringFunc(text, func(m string) string {
			p := iniPair.FindStringSubmatch(m)
			if !name(p[2]) || reference(p[4]) {
				return m
			}
			r.note(src, p[2])
			return p[1] + p[2] + p[3]
		})
	case base == ".bashrc" || base == ".bash_aliases" || base == ".profile" || strings.HasSuffix(base, ".sh") ||
		strings.Contains(filepath.ToSlash(src), "/.bashrc.d/"):
		text = shellAssign.ReplaceAllStringFunc(text, func(m string) string {
			p := shellAssign.FindStringSubmatch(m)
			if !name(p[2]) || reference(strings.TrimSpace(p[3])) {
				return m
			}
			r.note(src, p[2])
			return p[1] + p[2] + "="
		})
	}
	text = flagValue.ReplaceAllStringFunc(text, func(m string) string {
		p := flagValue.FindStringSubmatch(m)
		if !name(p[1]) || reference(p[2]) {
			return m
		}
		r.note(src, p[1])
		return p[1] + "="
	})
	if urlUserinfo.MatchString(text) {
		r.note(src, "URL credentials")
		text = urlUserinfo.ReplaceAllString(text, "$1")
	}
	if secretValue.MatchString(text) {
		r.note(src, "token value")
		text = secretValue.ReplaceAllString(text, "")
	}
	return text
}

// Summary describes up to limit redactions for display.
func Summary(redacted []string, limit int) string {
	if len(redacted) <= limit {
		return strings.Join(redacted, "\n")
	}
	return strings.Join(redacted[:limit], "\n") + fmt.Sprintf("\n... and %d more", len(redacted)-limit)
}
