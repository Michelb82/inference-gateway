package inference

import (
	"testing"
)

func TestParseVendor(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", wantErr: true},
		{in: "  ", wantErr: true},
		{in: "ollama", want: VendorOllama},
		{in: "Ollama", want: VendorOllama},
		{in: "openai", want: VendorOpenAI},
		{in: "OpenAI", want: VendorOpenAI},
		{in: "vllm", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseVendor(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParseVendor(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseVendor(%q) error = %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseVendor(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestUniqueTargetsAllowsEmptyOpenAIModel(t *testing.T) {
	got := uniqueTargets([]ModelTarget{
		{Vendor: VendorOpenAI, BaseURL: "https://someurl", Name: ""},
		{Vendor: VendorOllama, BaseURL: "http://llm", Name: ""},
		{Vendor: VendorOllama, BaseURL: "http://llm", Name: "qwen"},
		{Vendor: "", BaseURL: "http://llm", Name: "qwen"},
	})
	if len(got) != 2 {
		t.Fatalf("uniqueTargets = %#v, want 2 (openai empty name + ollama named)", got)
	}
	if got[0].Vendor != VendorOpenAI || got[0].Name != "" {
		t.Fatalf("first = %#v", got[0])
	}
	if got[1].Vendor != VendorOllama || got[1].Name != "qwen" {
		t.Fatalf("second = %#v", got[1])
	}
}
