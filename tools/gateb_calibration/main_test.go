package main

import "testing"

func TestParseArgumentsAcceptsOptionalThemeFilter(t *testing.T) {
	values, err := parseArguments([]string{
		"--index", "/tmp/index.json",
		"--out", "/tmp/output",
		"--root", "/tmp/state",
		"--only-theme", "100001",
	})
	if err != nil {
		t.Fatalf("parseArguments() error = %v", err)
	}
	if values["--only-theme"] != "100001" {
		t.Fatalf("theme filter = %q", values["--only-theme"])
	}
}

func TestParseArgumentsRejectsDuplicateAndUnknownOptions(t *testing.T) {
	for _, args := range [][]string{
		{
			"--index", "/tmp/index.json",
			"--out", "/tmp/output",
			"--root", "/tmp/state",
			"--root", "/tmp/other",
		},
		{
			"--index", "/tmp/index.json",
			"--out", "/tmp/output",
			"--root", "/tmp/state",
			"--theme", "100001",
		},
	} {
		if _, err := parseArguments(args); err == nil {
			t.Fatalf("parseArguments(%v) unexpectedly succeeded", args)
		}
	}
}

func TestSelectVariantsFiltersExactlyOneTheme(t *testing.T) {
	index := calibrationIndex{Variants: []variant{
		{ThemePublicID: "100001"},
		{ThemePublicID: "100002"},
		{ThemePublicID: "100003"},
	}}
	if err := selectVariants(&index, "100001"); err != nil {
		t.Fatalf("selectVariants() error = %v", err)
	}
	if len(index.Variants) != 1 || index.Variants[0].ThemePublicID != "100001" {
		t.Fatalf("filtered variants = %#v", index.Variants)
	}
}

func TestSelectVariantsRejectsMissingOrMalformedTheme(t *testing.T) {
	for _, onlyTheme := range []string{"100004", "10001"} {
		index := calibrationIndex{Variants: []variant{{ThemePublicID: "100001"}}}
		if err := selectVariants(&index, onlyTheme); err == nil {
			t.Fatalf("selectVariants(%q) unexpectedly succeeded", onlyTheme)
		}
	}
}
