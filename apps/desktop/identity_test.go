package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The bundle identity is build metadata, not code, so these tests pin the
// templates the Wails CLI renders: a stray com.wails.* id or a non-numeric
// productVersion would only surface at notarization / NSIS time.

func TestInfoPlist_UsesTheCalendiumBundleIdentity(t *testing.T) {
	b, err := os.ReadFile("build/darwin/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"<string>app.calendium.desktop</string>",
		"<string>app.calendium.desktop.{{.Scheme}}</string>",
		"<string>{{.Info.ProductVersion}}</string>",
		"<string>{{.Info.Copyright}}</string>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Info.plist missing %s", want)
		}
	}
	if strings.Contains(s, "com.wails.") {
		t.Errorf("Info.plist still carries a com.wails.* identifier")
	}
}

func TestWailsJSON_CarriesCompanyCopyrightAndNumericVersion(t *testing.T) {
	b, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Info struct {
			ProductName    string `json:"productName"`
			ProductVersion string `json:"productVersion"`
			CompanyName    string `json:"companyName"`
			Copyright      string `json:"copyright"`
			Protocols      []struct {
				Scheme string `json:"scheme"`
			} `json:"protocols"`
		} `json:"info"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Info.ProductName != "Calendium" || cfg.Info.CompanyName != "Calendium" {
		t.Errorf("productName/companyName = %q/%q, want Calendium/Calendium", cfg.Info.ProductName, cfg.Info.CompanyName)
	}
	if cfg.Info.Copyright != "© 2026 Calendium" {
		t.Errorf("copyright = %q, want %q", cfg.Info.Copyright, "© 2026 Calendium")
	}
	// NSIS VIProductVersion needs "${INFO_PRODUCTVERSION}.0" to be numeric.
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(cfg.Info.ProductVersion) {
		t.Errorf("productVersion = %q, want X.Y.Z", cfg.Info.ProductVersion)
	}
	if len(cfg.Info.Protocols) != 1 || cfg.Info.Protocols[0].Scheme != "calendium" {
		t.Errorf("protocols = %+v, want exactly the calendium scheme", cfg.Info.Protocols)
	}
}
