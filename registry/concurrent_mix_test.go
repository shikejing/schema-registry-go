package registry

import (
	"sync"
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func TestConcurrentReadersDuringRegistration(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers constantly fetch latest / list versions / validate; they must
	// never observe a half-written version or a gap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if versions, err := r.ListVersions("s", ""); err == nil {
				for i, v := range versions {
					if v != i+1 {
						t.Errorf("observed a gap: %v", versions)
						return
					}
				}
				if len(versions) > 0 {
					latest, err := r.GetLatest("s", "")
					if err != nil {
						t.Errorf("get latest: %v", err)
						return
					}
					if latest.Fingerprint == "" || latest.Schema == nil {
						t.Errorf("observed a half-written version: %+v", latest)
						return
					}
				}
			}
		}
	}()

	const writers, perWriter = 8, 25
	var rwg sync.WaitGroup
	for w := 0; w < writers; w++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for range perWriter {
				s := &schema.Schema{ID: "base", Name: "n", Fields: []*schema.Field{
					{Name: "core", Type: schema.TypeString, Required: false},
					{Name: "extra", Type: schema.TypeString, Required: false},
				}}
				_, _ = r.Register("s", s, "") // duplicate optional schema, always compatible
			}
		}()
	}
	rwg.Wait()
	close(stop)
	wg.Wait()

	versions, err := r.ListVersions("s", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != writers*perWriter {
		t.Fatalf("expected %d versions, got %d", writers*perWriter, len(versions))
	}
}
