package profiling

import "testing"

func TestReadMemory(t *testing.T) {
	p, s := readMemory()
	if p.Error != "" || s.Error != "" || p.ResidentBytes == 0 || s.TotalBytes == 0 || s.AvailableBytes > s.TotalBytes {
		t.Fatalf("memory metrics: %+v, %+v", p, s)
	}
}

func TestParseDRM(t *testing.T) {
	device, client, resident, available, err := parseDRM("drm-pdev:\t0000:01:00.0\ndrm-client-id: 23\ndrm-total-vram: 16 MiB\ndrm-resident-vram: 3 MiB\ndrm-resident-gtt: 8 KiB\ndrm-shared-vram: 1 MiB\n")
	if err != nil || !available || device != "0000:01:00.0" || client != "23" || resident != 3<<20+8<<10 {
		t.Fatalf("DRM snapshot: %s %s %d %t %v", device, client, resident, available, err)
	}
	_, _, _, available, err = parseDRM("drm-client-id: 23\ndrm-total-vram: 16 MiB\n")
	if err != nil || available {
		t.Fatal("requested storage was reported as resident")
	}
	if _, _, _, _, err := parseDRM("drm-resident-vram: unknown KiB\n"); err == nil {
		t.Fatal("invalid resident value accepted")
	}
}
