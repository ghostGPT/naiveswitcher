package service

import "testing"

func TestGetLatestLocalNaiveVersion(t *testing.T) {
	cases := []struct {
		arr      []string
		expected string
	}{
		{[]string{"naiveproxy-v0.0.1"}, "naiveproxy-v0.0.1"},
		{[]string{"naiveproxy-v0.0.1", "naiveproxy-v0.0.2"}, "naiveproxy-v0.0.2"},
		{[]string{"naiveproxy-v0.0.2", "naiveproxy-v0.0.1"}, "naiveproxy-v0.0.2"},
		{[]string{"naiveproxy-v0.0.1", "naiveproxy", "naiveproxy1"}, "naiveproxy-v0.0.1"},
		{[]string{"naiveproxy-v0.0.1", "naiveproxy-v0.0.2", "naiveproxy1"}, "naiveproxy-v0.0.2"},
		{[]string{"naiveproxy-v0.0.1", "naiveproxy-v0.0.3", "naiveproxy-v0.0.1", "naiveproxy1"}, "naiveproxy-v0.0.3"},
	}
	for _, c := range cases {
		actual, err := getLatestLocalNaiveVersion(c.arr)
		if err != nil {
			t.Errorf("Error: %v", err)
		}
		if actual != c.expected {
			t.Errorf("Expected %s, but got %s", c.expected, actual)
		}
	}
}

func TestGetNaiveOsArchSuffix(t *testing.T) {
	cases := []struct {
		fileName string
		expected string
	}{
		{"naiveproxy-v0.0.1-5-mac-x64", "mac-x64"},
		{"naiveproxy-v0.0.1-5-mac-x64-1", "mac-x64-1"},
		{"naiveproxy-v0.0.1-5-mac-x64-1-2.exe", "mac-x64-1-2.exe"},
	}
	for _, c := range cases {
		actual, err := getNaiveOsArchSuffix(c.fileName)
		if err != nil {
			t.Errorf("Error: %v", err)
		}
		if actual != c.expected {
			t.Errorf("Expected %s, but got %s", c.expected, actual)
		}
	}
}

func TestAssetUrlToBinaryName(t *testing.T) {
	cases := []struct {
		url      string
		expected string
	}{
		{"https://github.com/a.tar.gz", "a"},
		{"https://github.com/a", "a"},
		{"https://github.com/a.zip", "a"},
		{"https://github.com/a.tar", "a"},
	}
	for _, c := range cases {
		actual := assetUrlToBinaryName(c.url)
		if actual != c.expected {
			t.Errorf("Expected %s, but got %s", c.expected, actual)
		}
	}
}
