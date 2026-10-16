package main

import (
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    config
		wantErr bool
	}{
		{
			name: "defaults",
			want: config{addr: ":8080", model: model{revisionInterval: time.Hour, revisionRate: 0.02, revisionHorizon: 7 * 24 * time.Hour}},
		},
		{
			name: "overrides",
			env:  map[string]string{"FAKE_ENTSOE_ADDR": ":9000", "FAKE_ENTSOE_REVISION_INTERVAL": "5m", "FAKE_ENTSOE_REVISION_RATE": "0"},
			want: config{addr: ":9000", model: model{revisionInterval: 5 * time.Minute, revisionRate: 0, revisionHorizon: 7 * 24 * time.Hour}},
		},
		{name: "interval below a second", env: map[string]string{"FAKE_ENTSOE_REVISION_INTERVAL": "500ms"}, wantErr: true},
		{name: "unparsable interval", env: map[string]string{"FAKE_ENTSOE_REVISION_INTERVAL": "hourly"}, wantErr: true},
		{name: "rate above one", env: map[string]string{"FAKE_ENTSOE_REVISION_RATE": "1.5"}, wantErr: true},
		{name: "negative rate", env: map[string]string{"FAKE_ENTSOE_REVISION_RATE": "-0.1"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadConfig(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("loadConfig error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && cfg != tt.want {
				t.Errorf("got %+v, want %+v", cfg, tt.want)
			}
		})
	}
}
