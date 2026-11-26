package main

import (
	"slices"
	"testing"

	"github.com/onix-engine/onix/project"
)

func TestPencodeModuleProject(t *testing.T) {
	plan, err := project.CompileProjectDirectory(".")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Spec.Entry != "main.json" || len(plan.Scenes) != 2 || len(plan.Mods) != 1 {
		t.Fatalf("unexpected module plan: entry=%q scenes=%d mods=%d", plan.Spec.Entry, len(plan.Scenes), len(plan.Mods))
	}
	// The editor rasterizes this SVG at 4x when it first paints a diagnostic.
	const wave = "resources/workbench/assets/ui/squiggly.svg"
	if !slices.ContainsFunc(plan.Mods[0].Resources, func(r project.ModResourcePlan) bool { return r.Path == wave }) {
		t.Fatal("release resource plan omits the diagnostic underline")
	}
	if engine := plan.Spec.Engine; engine.TextureCompression && !slices.Contains(engine.TextureCompressionExcludes, wave) {
		t.Fatal("release texture compression would replace the diagnostic underline SVG")
	}
}
