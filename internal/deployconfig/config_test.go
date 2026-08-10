package deployconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestComposeContainsRequiredServicesAndSecrets(t *testing.T) {
	root := filepath.Join("..", "..")
	var compose struct {
		Services map[string]struct {
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
		Secrets map[string]any `yaml:"secrets"`
	}
	readYAML(t, filepath.Join(root, "compose.yml"), &compose)
	for _, service := range []string{"erlang-exporter", "prometheus", "alertmanager", "grafana"} {
		if _, exists := compose.Services[service]; !exists {
			t.Errorf("compose service %q is missing", service)
		}
	}
	for _, secret := range []string{"monitor_private_key", "monitor_private_key_passphrase", "dingtalk_webhook_url", "dingtalk_secret", "grafana_admin_password"} {
		if _, exists := compose.Secrets[secret]; !exists {
			t.Errorf("compose secret %q is missing", secret)
		}
	}
	if _, exists := compose.Services["dingtalk-adapter"]; exists {
		t.Fatal("standalone dingtalk-adapter service should remain removed; exporter owns the webhook")
	}
	if !contains(compose.Services["grafana"].Volumes, "./grafana/dashboards-internal:/var/lib/grafana/dashboards-internal:ro") {
		t.Fatal("grafana must mount the isolated qt-01 internal dashboard directory")
	}
	if !contains(compose.Services["grafana"].Volumes, "./grafana/dashboards-qt05-internal:/var/lib/grafana/dashboards-qt05-internal:ro") {
		t.Fatal("grafana must mount the isolated qt-05 internal dashboard directory")
	}
}

func TestMonitoringPortsUseReserved20900Range(t *testing.T) {
	root := filepath.Join("..", "..")
	files := []string{
		filepath.Join(root, "compose.yml"),
		filepath.Join(root, "scripts", "start-local-monitor.ps1"),
		filepath.Join(root, "scripts", "start-holmes-local.ps1"),
	}
	combined := ""
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		combined += string(data)
	}
	for _, port := range []string{"20900", "20901", "20902", "20903", "20904", "20905"} {
		if !strings.Contains(combined, port) {
			t.Errorf("reserved monitoring port %s is missing", port)
		}
	}
	if strings.Contains(combined, "5050") {
		t.Fatal("legacy Holmes port 5050 must not be used")
	}
}

func TestInventoryKeepsDeploymentDisabled(t *testing.T) {
	root := filepath.Join("..", "..")
	var inventory struct {
		Nodes []struct {
			Address   string `yaml:"address"`
			Role      string `yaml:"role"`
			DeployNow bool   `yaml:"deploy_now"`
		} `yaml:"monitoring_nodes"`
	}
	readYAML(t, filepath.Join(root, "docs", "deployment", "inventory.yml"), &inventory)
	if len(inventory.Nodes) != 2 || inventory.Nodes[0].Address != "192.168.100.22" || inventory.Nodes[1].Address != "192.168.100.24" {
		t.Fatalf("unexpected inventory: %#v", inventory.Nodes)
	}
	for _, node := range inventory.Nodes {
		if node.DeployNow {
			t.Errorf("deployment must remain disabled for %s", node.Address)
		}
	}
}

func TestGrafanaDashboardIsValidJSON(t *testing.T) {
	path := filepath.Join("..", "..", "grafana", "dashboards", "erlang-overview.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dashboard map[string]any
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatal(err)
	}
	if dashboard["uid"] != "erlang-monitor-overview" {
		t.Fatalf("unexpected dashboard uid: %v", dashboard["uid"])
	}
}

func TestGrafanaDashboardsUseDynamicFolderNavigation(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "grafana", "dashboards", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no Grafana dashboards found")
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var dashboard struct {
			Tags  []string `json:"tags"`
			Links []struct {
				AsDropdown  bool     `json:"asDropdown"`
				IncludeVars bool     `json:"includeVars"`
				KeepTime    bool     `json:"keepTime"`
				Tags        []string `json:"tags"`
				Title       string   `json:"title"`
				Type        string   `json:"type"`
			} `json:"links"`
			Templating struct {
				List []struct {
					Hide int `json:"hide"`
				} `json:"list"`
			} `json:"templating"`
		}
		if err := json.Unmarshal(data, &dashboard); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if !contains(dashboard.Tags, "qt-01") {
			t.Errorf("%s must carry the qt-01 tag for dynamic navigation", path)
		}
		if len(dashboard.Links) != 1 {
			t.Errorf("%s links = %#v, want one dynamic dashboard link", path, dashboard.Links)
			continue
		}
		link := dashboard.Links[0]
		if link.Type != "dashboards" || link.Title != "目录-页面" || !link.AsDropdown || !link.KeepTime || link.IncludeVars || !contains(link.Tags, "qt-01") {
			t.Errorf("%s dynamic link = %#v", path, link)
		}
		if len(dashboard.Templating.List) != 1 || dashboard.Templating.List[0].Hide != 2 {
			t.Errorf("%s server filter must stay hidden behind dynamic page navigation", path)
		}
	}
}

func TestGrafanaInternalDashboardsStayIsolated(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "grafana", "dashboards-internal", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct {
		title string
		uid   string
	}{
		"192.168.100.23": {title: "192.168.100.23(debug)", uid: "erlang-monitor-internal-192-168-100-23"},
		"192.168.100.25": {title: "192.168.100.25(act)", uid: "erlang-monitor-internal-192-168-100-25"},
	}
	if len(paths) != len(expected) {
		t.Fatalf("internal dashboard count = %d, want %d", len(paths), len(expected))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var dashboard struct {
			Title string   `json:"title"`
			UID   string   `json:"uid"`
			Tags  []string `json:"tags"`
			Links []struct {
				Tags []string `json:"tags"`
			} `json:"links"`
			Templating struct {
				List []struct {
					Hide    int    `json:"hide"`
					Name    string `json:"name"`
					Current struct {
						Value string `json:"value"`
					} `json:"current"`
				} `json:"list"`
			} `json:"templating"`
		}
		if err := json.Unmarshal(data, &dashboard); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if len(dashboard.Templating.List) != 1 {
			t.Fatalf("%s must have one fixed server variable", path)
		}
		serverVariable := dashboard.Templating.List[0]
		want, exists := expected[serverVariable.Current.Value]
		if !exists {
			t.Fatalf("%s has unexpected server %q", path, serverVariable.Current.Value)
		}
		if dashboard.Title != want.title || dashboard.UID != want.uid || serverVariable.Name != "server" || serverVariable.Hide != 2 {
			t.Errorf("%s identity = title %q uid %q variable %#v", path, dashboard.Title, dashboard.UID, serverVariable)
		}
		if !contains(dashboard.Tags, "qt-01内网") || contains(dashboard.Tags, "qt-01") {
			t.Errorf("%s dashboard tags must be isolated: %#v", path, dashboard.Tags)
		}
		if len(dashboard.Links) != 1 || !contains(dashboard.Links[0].Tags, "qt-01内网") || contains(dashboard.Links[0].Tags, "qt-01") {
			t.Errorf("%s navigation tags must be isolated: %#v", path, dashboard.Links)
		}
	}
}

func TestGrafanaQt05InternalDashboardsStayIsolated(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "grafana", "dashboards-qt05-internal", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct {
		title string
		uid   string
	}{
		"192.168.100.33": {title: "192.168.100.33(debug)", uid: "erlang-qt05-192-168-100-33"},
		"192.168.100.37": {title: "192.168.100.37(act)", uid: "erlang-qt05-192-168-100-37"},
	}
	if len(paths) != len(expected) {
		t.Fatalf("qt-05 internal dashboard count = %d, want %d", len(paths), len(expected))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var dashboard struct {
			Title string   `json:"title"`
			UID   string   `json:"uid"`
			Tags  []string `json:"tags"`
			Links []struct {
				Tags []string `json:"tags"`
			} `json:"links"`
			Templating struct {
				List []struct {
					Hide    int    `json:"hide"`
					Name    string `json:"name"`
					Current struct {
						Value string `json:"value"`
					} `json:"current"`
				} `json:"list"`
			} `json:"templating"`
		}
		if err := json.Unmarshal(data, &dashboard); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if len(dashboard.Templating.List) != 1 {
			t.Fatalf("%s must have one fixed server variable", path)
		}
		serverVariable := dashboard.Templating.List[0]
		want, exists := expected[serverVariable.Current.Value]
		if !exists {
			t.Fatalf("%s has unexpected server %q", path, serverVariable.Current.Value)
		}
		if dashboard.Title != want.title || dashboard.UID != want.uid || serverVariable.Name != "server" || serverVariable.Hide != 2 {
			t.Errorf("%s identity = title %q uid %q variable %#v", path, dashboard.Title, dashboard.UID, serverVariable)
		}
		if len(dashboard.UID) > 40 {
			t.Errorf("%s dashboard UID is %d characters; Grafana allows at most 40", path, len(dashboard.UID))
		}
		if !contains(dashboard.Tags, "qt-05内网") || contains(dashboard.Tags, "qt-01内网") || contains(dashboard.Tags, "qt-01") {
			t.Errorf("%s dashboard tags must be isolated: %#v", path, dashboard.Tags)
		}
		if len(dashboard.Links) != 1 || !contains(dashboard.Links[0].Tags, "qt-05内网") || contains(dashboard.Links[0].Tags, "qt-01内网") || contains(dashboard.Links[0].Tags, "qt-01") {
			t.Errorf("%s navigation tags must be isolated: %#v", path, dashboard.Links)
		}
	}
}

func TestGrafanaInternalProviderUsesRequestedFolderUID(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: filepath.Join("..", "..", "grafana", "provisioning-local", "dashboards", "dashboards.yml"), want: "${ERLANG_MONITOR_INTERNAL_DASHBOARDS_PATH}"},
		{path: filepath.Join("..", "..", "grafana", "provisioning", "dashboards", "dashboards.yml"), want: "/var/lib/grafana/dashboards-internal"},
	}
	for _, test := range tests {
		var provisioning struct {
			Providers []struct {
				Name      string         `yaml:"name"`
				Folder    string         `yaml:"folder"`
				FolderUID string         `yaml:"folderUid"`
				Options   map[string]any `yaml:"options"`
			} `yaml:"providers"`
		}
		readYAML(t, test.path, &provisioning)
		var internal *struct {
			Name      string         `yaml:"name"`
			Folder    string         `yaml:"folder"`
			FolderUID string         `yaml:"folderUid"`
			Options   map[string]any `yaml:"options"`
		}
		for i := range provisioning.Providers {
			if provisioning.Providers[i].Name == "qt-01 Internal Monitoring" {
				internal = &provisioning.Providers[i]
				break
			}
		}
		if internal == nil {
			t.Fatalf("%s is missing the internal provider", test.path)
		}
		if internal.Folder != "qt-01内网" || internal.FolderUID != "dfu4oqx4rqmm8f" || internal.Options["path"] != test.want {
			t.Errorf("%s internal provider = %#v", test.path, internal)
		}
	}
}

func TestGrafanaQt05InternalProviderUsesRequestedFolderUID(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: filepath.Join("..", "..", "grafana", "provisioning-local", "dashboards", "dashboards.yml"), want: "${ERLANG_MONITOR_QT05_INTERNAL_DASHBOARDS_PATH}"},
		{path: filepath.Join("..", "..", "grafana", "provisioning", "dashboards", "dashboards.yml"), want: "/var/lib/grafana/dashboards-qt05-internal"},
	}
	for _, test := range tests {
		var provisioning struct {
			Providers []struct {
				Name      string         `yaml:"name"`
				Folder    string         `yaml:"folder"`
				FolderUID string         `yaml:"folderUid"`
				Options   map[string]any `yaml:"options"`
			} `yaml:"providers"`
		}
		readYAML(t, test.path, &provisioning)
		found := false
		for _, provider := range provisioning.Providers {
			if provider.Name != "qt-05 Internal Monitoring" {
				continue
			}
			found = true
			if provider.Folder != "qt-05内网" || provider.FolderUID != "dfu56gegpvqbkc" || provider.Options["path"] != test.want {
				t.Errorf("%s qt-05 internal provider = %#v", test.path, provider)
			}
		}
		if !found {
			t.Fatalf("%s is missing the qt-05 internal provider", test.path)
		}
	}
}

func TestGrafanaDashboardShowsLatestCollectionAndExpandedAnomalies(t *testing.T) {
	path := filepath.Join("..", "..", "grafana", "dashboards", "erlang-overview.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		Refresh    string `json:"refresh"`
		Timepicker struct {
			Hidden           bool     `json:"hidden"`
			RefreshIntervals []string `json:"refresh_intervals"`
		} `json:"timepicker"`
		Panels []struct {
			Title     string `json:"title"`
			Type      string `json:"type"`
			Collapsed bool   `json:"collapsed"`
			Targets   []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
			Panels []struct {
				Title string `json:"title"`
			} `json:"panels"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Panels) != 7 || dashboard.Panels[0].Title != "最近一次采集时间" {
		t.Fatalf("dashboard daily layout = %#v, want collection time plus two main panels and expanded anomaly panels", dashboard.Panels)
	}
	if dashboard.Panels[3].Type != "row" || dashboard.Panels[3].Collapsed || len(dashboard.Panels[3].Panels) != 0 {
		t.Fatalf("anomaly row = %#v, want expanded row with child panels at top level", dashboard.Panels[3])
	}
	if dashboard.Refresh != "30m" || dashboard.Timepicker.Hidden || len(dashboard.Timepicker.RefreshIntervals) != 1 || dashboard.Timepicker.RefreshIntervals[0] != "30m" {
		t.Fatalf("dashboard refresh controls = refresh %q, hidden %v, intervals %#v; want 30m refresh interval", dashboard.Refresh, dashboard.Timepicker.Hidden, dashboard.Timepicker.RefreshIntervals)
	}
	if len(dashboard.Panels[0].Targets) != 1 || !strings.Contains(dashboard.Panels[0].Targets[0].Expr, "erlang_exporter_last_success_timestamp_seconds") {
		t.Fatal("dashboard should show the latest successful collection timestamp")
	}
	var hasRegisteredUsers, hasOnlineUsers, hasHostCapacity bool
	for _, panel := range dashboard.Panels {
		for _, target := range panel.Targets {
			hasRegisteredUsers = hasRegisteredUsers || strings.Contains(target.Expr, "erlang_game_registered_users")
			hasOnlineUsers = hasOnlineUsers || strings.Contains(target.Expr, "erlang_game_online_users")
			hasHostCapacity = hasHostCapacity || strings.Contains(target.Expr, "erlang_host_memory_total_bytes") && strings.Contains(target.Expr, "erlang_host_memory_available_bytes")
		}
	}
	if !hasRegisteredUsers || !hasOnlineUsers {
		t.Fatal("dashboard should expose pending registered and online player count columns")
	}
	if !hasHostCapacity {
		t.Fatal("dashboard should expose host memory capacity fields")
	}
}

func readYAML(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
