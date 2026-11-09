package appupdate

import "testing"

func TestPlatformReleaseNeverInstallsAnotherPlatform(t *testing.T) {
	r := fixtureRelease("0.2.0", false)
	windowsAsset := r.Assets[0]
	windowsAsset.Name = "Cage-0.2.0-windows-amd64-setup.exe"
	windowsAsset.URL = "https://github.com/" + repository + "/releases/download/v0.2.0/" + windowsAsset.Name
	r.Assets = append(r.Assets, windowsAsset)
	for _, tc := range []struct{ os, arch, name string }{
		{"windows", "amd64", windowsAsset.Name},
		{"darwin", "arm64", r.Assets[0].Name},
	} {
		c, err := selectPlatformRelease([]release{r}, "0.1.0", tc.os, tc.arch)
		if err != nil || c == nil || c.Archive.Name != tc.name {
			t.Fatalf("%s/%s: %#v %v", tc.os, tc.arch, c, err)
		}
	}
	if c, err := selectPlatformRelease([]release{fixtureRelease("0.2.0", false)}, "0.1.0", "windows", "amd64"); err != nil || c != nil {
		t.Fatalf("macOS package selected on Windows: %#v %v", c, err)
	}
	if _, err := selectPlatformRelease([]release{r}, "0.1.0", "windows", "arm64"); err == nil {
		t.Fatal("unsupported architecture appeared up to date")
	}
}
