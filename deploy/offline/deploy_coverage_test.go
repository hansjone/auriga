package main

import (
	"testing"
)

func TestSiteServicesCoverage(t *testing.T) {
	root := repoRoot(t)
	spec, err := loadSiteServices(root)
	if err != nil {
		t.Fatal(err)
	}
	programs, err := loadGoPrograms(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := gitTrackedFiles(root)
	if err != nil {
		t.Skip("本机无法 git ls-files：", err)
	}
	covered, err := expandInputCoverage(root, spec, programs)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkCoverage(files, covered, spec.Ignore); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageNegativeUnclassified(t *testing.T) {
	covered := map[string]bool{"a.go": true}
	if err := checkCoverage([]string{"a.go", "orphan.txt"}, covered, nil); err == nil {
		t.Fatal("未归类文件应失败")
	}
}
