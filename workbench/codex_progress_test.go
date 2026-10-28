package workbench

import (
	"os"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/scene"
	notosanssc "github.com/diiyw/onix/testdata/fonts/Noto_Sans_SC"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestCodexActivitySweepRetainsTextWithoutAllocations(t *testing.T) {
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	world := ecs.NewWorld()
	clock := scene.NewTime()
	ecs.InsertResource(world, clock)
	view := htmlui.New(`<style>` + string(css) + `</style><article class="codex-chat"><button id="activity" class="codex-activity-summary" type="button" aria-expanded="false"><span class="codex-sweep-text" style="color:#cccccc"><span style="animation-delay:0ms">正</span><span style="animation-delay:65ms">在</span><span style="animation-delay:130ms">搜</span><span style="animation-delay:195ms">索</span></span></button></article>`)
	view.Width, view.Height, view.Font = 640, 240, notosanssc.NotoSansSCRegular
	bundle := ecs.NewBundle(1)
	ecs.Put(&bundle, view)
	owner := world.Spawn(bundle)
	sync := func(delta time.Duration) {
		clock.Set(delta, clock.Elapsed+delta, clock.Frame+1)
		if err := htmlui.Sync(world); err != nil {
			panic(err)
		}
	}
	sync(0)
	if !htmlui.Animating(world) {
		t.Fatal("activity does not schedule animation frames")
	}
	var letters [4]ecs.Entity
	for entity, access := range ecs.Query[scene.Text2D](world).All {
		switch access.Ref().Value {
		case "正":
			letters[0] = entity
		case "在":
			letters[1] = entity
		case "搜":
			letters[2] = entity
		case "索":
			letters[3] = entity
		}
	}
	for _, entity := range letters {
		if entity == 0 {
			t.Fatal("missing animated text", letters)
		}
	}
	// The first letter reaches its peak before the last letter.
	sync(480 * time.Millisecond)
	first, _ := ecs.Get[scene.Text2D](world, letters[0])
	last, _ := ecs.Get[scene.Text2D](world, letters[3])
	if first.Alpha < .99 || last.Alpha >= first.Alpha {
		t.Fatalf("sweep did not begin on the left: first=%g last=%g", first.Alpha, last.Alpha)
	}
	sync(195 * time.Millisecond)
	first, _ = ecs.Get[scene.Text2D](world, letters[0])
	last, _ = ecs.Get[scene.Text2D](world, letters[3])
	if last.Alpha < .99 || first.Alpha >= last.Alpha {
		t.Fatalf("sweep did not move right: first=%g last=%g", first.Alpha, last.Alpha)
	}
	if allocations := testing.AllocsPerRun(100, func() { sync(16 * time.Millisecond) }); allocations != 0 {
		t.Fatalf("warm activity animation allocated %g objects per frame", allocations)
	}
	// Removing the indicator must allow the frame scheduler to return to idle.
	node, ok := htmlui.NodeByID(world, owner, "activity")
	if !ok || !node.Remove() {
		t.Fatal("could not remove completed activity")
	}
	sync(0)
	if htmlui.Animating(world) {
		t.Fatal("completed activity kept animation frames alive")
	}
}
