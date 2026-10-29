package workbench

import (
	"os"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestTabBusyDotsAnimateWithoutAllocations(t *testing.T) {
	css, err := os.ReadFile("../resources/workbench/assets/ui/pencode.css")
	if err != nil {
		t.Fatal(err)
	}
	world := ecs.NewWorld()
	clock := scene.NewTime()
	world.InsertResource(clock)
	view := htmlui.New(`<style>` + string(css) + `</style><span id="busy" class="pencode-tab-busy-icon" style="color:#70c9df"><span></span><span></span><span></span><span></span><span></span><span></span><span></span><span></span></span>`)
	view.Width, view.Height = 100, 100
	bundle := ecs.NewBundle(1)
	bundle.Put(view)
	owner := world.Spawn(bundle)
	advance := func() {
		clock.Set(16*time.Millisecond, clock.Elapsed+16*time.Millisecond, clock.Frame+1)
		if err := htmlui.Sync(world); err != nil {
			panic(err)
		}
	}
	advance()
	var dots [8]ecs.Entity
	count := 0
	for entity, access := range world.Query[scene.Sprite2D]().All {
		if sprite := access.Ref(); sprite.Width == 2 && sprite.Height == 2 {
			if count == len(dots) {
				t.Fatal("too many dot visuals")
			}
			dots[count] = entity
			count++
		}
	}
	if count != len(dots) || !htmlui.Animating(world) {
		t.Fatalf("missing animated dots: %d", count)
	}
	first, _ := world.Get[scene.Sprite2D](dots[0])
	for range 8 {
		advance()
	}
	after, _ := world.Get[scene.Sprite2D](dots[0])
	if first.Alpha == after.Alpha {
		t.Fatal("dot brightness did not advance")
	}
	if allocations := testing.AllocsPerRun(100, advance); allocations != 0 {
		t.Fatalf("warm dot animation allocated %g objects per frame", allocations)
	}
	node, _ := htmlui.NodeByID(world, owner, "busy")
	node.Remove()
	advance()
	if htmlui.Animating(world) {
		t.Fatal("removed busy icon kept scheduling frames")
	}
}
