package deployment

import "testing"

func TestSpecValidate(t *testing.T) {
	valid := Spec{
		ParentInterface: "eth1",
		ContainerIP:     "192.168.2.241",
		Subnet:          "192.168.2.0/24",
		Gateway:         "192.168.2.1",
		DataPath:        "/share/Container/opensurge",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Spec)
	}{
		{"bad interface", func(s *Spec) { s.ParentInterface = "eth1;rm" }},
		{"ip outside subnet", func(s *Spec) { s.ContainerIP = "10.0.0.2" }},
		{"gateway outside subnet", func(s *Spec) { s.Gateway = "10.0.0.1" }},
		{"same ip gateway", func(s *Spec) { s.ContainerIP = s.Gateway }},
		{"network address", func(s *Spec) { s.ContainerIP = "192.168.2.0" }},
		{"broadcast address", func(s *Spec) { s.ContainerIP = "192.168.2.255" }},
		{"relative data", func(s *Spec) { s.DataPath = "./data" }},
		{"unsafe data root", func(s *Spec) { s.DataPath = "/etc/opensurge" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valid
			tt.edit(&got)
			if err := got.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSpecNormalizeDefaultDataPath(t *testing.T) {
	s := Spec{DataPath: ""}
	s.Normalize()
	if s.DataPath != DefaultGatewayDataPath {
		t.Fatalf("DataPath=%q, want %q", s.DataPath, DefaultGatewayDataPath)
	}
}
