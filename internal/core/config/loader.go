package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envPattern = regexp.MustCompile(`\$\{[^}]+\}`)

func LoadYAML[T any](path string) (T, error) {
	var zero T
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, err
	}
	data, err = expandEnv(data)
	if err != nil {
		return zero, err
	}
	return unmarshalYAML[T](data)
}

func expandEnv(data []byte) ([]byte, error) {
	text := string(data)
	missing := make([]string, 0)
	text = envPattern.ReplaceAllStringFunc(text, func(token string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(token, "${"), "}")
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return token
		}
		return val
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("env vars not injected: %v", missing)
	}
	return []byte(text), nil
}

func unmarshalYAML[T any](data []byte) (T, error) {
	var dest T
	typ := reflect.TypeOf(dest)
	if typ != nil && typ.Kind() == reflect.Ptr {
		dest = reflect.New(typ.Elem()).Interface().(T)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&dest); err != nil {
		var zero T
		return zero, err
	}
	return dest, nil
}
