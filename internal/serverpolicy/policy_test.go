package serverpolicy

import "testing"

func TestDisabledServerIDs(t *testing.T) {
	policy, err := ParseDisabledServerIDs(" 7, 12,7 ")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{7, 12} {
		if !policy.IsDisabled(id) {
			t.Errorf("server %d must be disabled", id)
		}
	}
	if policy.IsDisabled(8) {
		t.Fatal("unlisted server must stay enabled")
	}
}

func TestDisabledServerIDsDefault(t *testing.T) {
	for _, raw := range []string{"", " \t\n"} {
		policy, err := ParseDisabledServerIDs(raw)
		if err != nil || policy.IsDisabled(7) {
			t.Errorf("empty configuration must allow existing servers: %v", err)
		}
	}
	if (Policy{}).IsDisabled(7) {
		t.Fatal("zero policy must preserve existing behavior")
	}
}

func TestDisabledServerIDsRejectMalformedConfiguration(t *testing.T) {
	for _, raw := range []string{"Estonia", "0", "-1", "7,", ",7", "7,,12", "7,broken", "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseDisabledServerIDs(raw); err == nil {
				t.Fatal("malformed configuration must not silently enable a retired server")
			}
		})
	}
}
