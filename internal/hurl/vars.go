package hurl

import (
	"fmt"
	"strings"
)

// ParseVariable parses name=value as hurl's --variable does. The name is
// everything before the first =. A value in double quotes is a string
// without them; any other value takes the type its text looks like, so
// 7 compares equal to the number 7 and "7" doesn't.
func ParseVariable(s string) (name string, value any, err error) {
	name, v, ok := strings.Cut(s, "=")
	if !ok {
		return "", nil, fmt.Errorf("missing value for variable %s", s)
	}
	if strings.HasPrefix(v, `"`) {
		if len(v) < 2 || !strings.HasSuffix(v, `"`) {
			return "", nil, fmt.Errorf("the value of %s should end with a double quote", name)
		}
		return name, v[1 : len(v)-1], nil
	}
	return name, envString(v), nil
}

// ParseVariables parses a variables file as hurl's --variables-file does:
// one name=value per line, each line trimmed first, with blank lines and
// lines starting with # skipped. Later lines win.
func ParseVariables(text string) (map[string]any, error) {
	vars := map[string]any{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, v, err := ParseVariable(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", i+1, err)
		}
		vars[name] = v
	}
	return vars, nil
}

// fromEnv types a value from the environment like a --variable value. A
// value with a stray opening quote stays as it is: the environment holds
// more than growl's variables.
func fromEnv(s string) any {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}
	return envString(s)
}
