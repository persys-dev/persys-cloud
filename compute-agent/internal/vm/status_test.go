package vm

import (
	"testing"
	"time"
)

func TestVMStatusBuilder(t *testing.T) {
	now := time.Now()
	st := NewVMStatusBuilder("vm1").WithState("running").WithResources(2, 2048).WithIPs([]string{"10.0.0.2"}).WithTimestamps(now, now).Build()
	if st.Name != "vm1" || st.State != "running" || len(st.IPAddresses) != 1 {
		t.Fatal("unexpected vm status builder result")
	}
}

func TestNetworkWaiter_WaitForIP(t *testing.T) {
	w := NewNetworkWaiter(2, time.Millisecond)
	ips, err := w.WaitForIP("vm1", func() ([]string, error) { return []string{"10.0.0.2"}, nil })
	if err != nil || len(ips) != 1 {
		t.Fatalf("expected ip, got ips=%v err=%v", ips, err)
	}
}
