package main

import (
	"testing"

	"github.com/diiyw/onix/project"
)

func TestPencodeModuleProject(t *testing.T) {
	plan, err := project.CompileProjectDirectory(".")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Spec.Entry != "main.json" || len(plan.Scenes) != 2 || len(plan.Mods) != 1 {
		t.Fatalf("unexpected module plan: entry=%q scenes=%d mods=%d", plan.Spec.Entry, len(plan.Scenes), len(plan.Mods))
	}
}
