package holmesgateway

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

type composeDocument struct {
	Services map[string]struct {
		Profiles []string `yaml:"profiles"`
		Secrets  []string `yaml:"secrets"`
	} `yaml:"services"`
	Secrets map[string]any `yaml:"secrets"`
}

func TestComposeDocumentsParseAndKeepHolmesOptional(t *testing.T) {
	root := filepath.Join("..", "..")
	base := readComposeDocument(t, filepath.Join(root, "compose.yml"))
	overlay := readComposeDocument(t, filepath.Join(root, "compose.holmes.yml"))

	for _, service := range []string{"holmes", "holmes-gateway"} {
		if profiles := base.Services[service].Profiles; len(profiles) != 1 || profiles[0] != "holmes" {
			t.Fatalf("%s must remain behind the optional holmes profile: %#v", service, profiles)
		}
	}
	grafana, ok := overlay.Services["grafana"]
	if !ok || !containsString(grafana.Secrets, "holmes_tool_api_token") {
		t.Fatalf("Holmes overlay must inject the internal Grafana proxy token: %#v", grafana.Secrets)
	}
	if _, ok := overlay.Secrets["holmes_tool_api_token"]; !ok {
		t.Fatal("Holmes overlay must declare holmes_tool_api_token")
	}
}

func readComposeDocument(t *testing.T, path string) composeDocument {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document composeDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return document
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
