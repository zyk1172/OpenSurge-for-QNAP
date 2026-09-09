package qnaphost

import "testing"

func TestValidateNetworkConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  NetworkConfig
		ok   bool
	}{
		{name: "valid", cfg: NetworkConfig{ParentInterface: "eth1", IPv4: "192.168.2.241", Subnet: "192.168.2.0/24", Gateway: "192.168.2.1"}, ok: true},
		{name: "bad interface", cfg: NetworkConfig{ParentInterface: "eth1;rm", IPv4: "192.168.2.241", Subnet: "192.168.2.0/24", Gateway: "192.168.2.1"}},
		{name: "outside subnet", cfg: NetworkConfig{ParentInterface: "eth1", IPv4: "192.168.3.241", Subnet: "192.168.2.0/24", Gateway: "192.168.2.1"}},
		{name: "gateway outside", cfg: NetworkConfig{ParentInterface: "eth1", IPv4: "192.168.2.241", Subnet: "192.168.2.0/24", Gateway: "10.0.0.1"}},
		{name: "same gateway", cfg: NetworkConfig{ParentInterface: "eth1", IPv4: "192.168.2.1", Subnet: "192.168.2.0/24", Gateway: "192.168.2.1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateNetworkConfig(test.cfg)
			if test.ok && err != nil {
				t.Fatalf("validate: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestSameNetworkRequiresExactQNETOwnershipShape(t *testing.T) {
	cfg := NetworkConfig{ParentInterface: "eth1", IPv4: "192.168.2.241", Subnet: "192.168.2.0/24", Gateway: "192.168.2.1"}
	network := dockerNetworkInspect{
		Driver:  "qnet",
		Options: map[string]string{"iface": "eth1"},
		IPAM: dockerIPAM{
			Driver:  "qnet",
			Options: map[string]string{"iface": "eth1"},
			Config:  []dockerIPAMConfig{{Subnet: "192.168.2.0/24", Gateway: "192.168.2.1"}},
		},
	}
	if !sameNetwork(network, cfg) {
		t.Fatal("expected exact qnet configuration to match")
	}
	network.Options["iface"] = "eth0"
	if sameNetwork(network, cfg) {
		t.Fatal("different parent interface must not match")
	}
}
