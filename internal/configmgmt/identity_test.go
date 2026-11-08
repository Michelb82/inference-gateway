package configmgmt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
)

func TestInstanceIdentityAndPolling(t *testing.T) {
	identity, err := configmgmt.NewInstanceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := configmgmt.NewInstanceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID == "" || identity.ID == other.ID {
		t.Fatalf("instance IDs must be nonempty and unique: %q, %q", identity.ID, other.ID)
	}

	requests := make(chan configmgmt.InstanceIdentity, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- configmgmt.InstanceIdentity{ID: r.Header.Get("X-Gateway-Instance-ID"), Hostname: r.Header.Get("X-Gateway-Hostname")}
		http.Error(w, "not registered", http.StatusForbidden)
	}))
	defer srv.Close()
	mgr := configmgmt.NewManager(identity, srv.URL, time.Second, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); mgr.Run(ctx) }()
	defer func() { cancel(); <-done }()
	for i := 0; i < 2; i++ {
		select {
		case got := <-requests:
			if got != identity {
				t.Fatalf("poll identity = %+v, want %+v", got, identity)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for poll")
		}
	}
	if mgr.HasSnapshot() {
		t.Fatal("rejected manifest must leave gateway dormant")
	}
}
