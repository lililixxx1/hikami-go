package publisher

import "testing"

func TestPublishTargetMarshalRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		t    PublishTarget
	}{
		{"published", PublishTarget{DynID: "dyn1", DynType: 2, DynRid: "rid1"}},
		{"draft", PublishTarget{DraftID: "d1"}},
		{"empty", PublishTarget{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.t.Marshal()
			parsed := ParsePublishTarget(s)
			if parsed != c.t {
				t.Errorf("round-trip mismatch:\nmarshal=%s\ngot=%+v\nwant=%+v", s, parsed, c.t)
			}
		})
	}
}

func TestParsePublishTargetLegacyFormats(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  PublishTarget
	}{
		{"empty", "", PublishTarget{}},
		{"legacy draft", "draft:abc", PublishTarget{DraftID: "abc"}},
		{"legacy bare dyn_id", "123456789", PublishTarget{DynID: "123456789"}},
		{"new json published", `{"dyn_id":"d1","dyn_type":2,"dyn_rid":"r1"}`, PublishTarget{DynID: "d1", DynType: 2, DynRid: "r1"}},
		{"new json draft", `{"draft_id":"x"}`, PublishTarget{DraftID: "x"}},
		{"whitespace bare", "  999  ", PublishTarget{DynID: "999"}},
		{"invalid json falls back to bare", "{not json", PublishTarget{DynID: "{not json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParsePublishTarget(c.input)
			if got != c.want {
				t.Errorf("ParsePublishTarget(%q) = %+v, want %+v", c.input, got, c.want)
			}
		})
	}
}
