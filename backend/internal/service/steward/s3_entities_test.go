package steward

import "testing"

func TestEntityTagSyncEntityIDIsStable(t *testing.T) {
	first := entityTagSyncEntityID("memory", "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	second := entityTagSyncEntityID("memory", "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	different := entityTagSyncEntityID("memory", "11111111-1111-1111-1111-111111111111", "33333333-3333-3333-3333-333333333333")

	if first == "" {
		t.Fatalf("expected derived id")
	}
	if first != second {
		t.Fatalf("derived id is not stable: %s != %s", first, second)
	}
	if first == different {
		t.Fatalf("derived id should change when tag id changes")
	}
}

func TestDeviceRevocationSyncEntityIDIsStable(t *testing.T) {
	first := deviceRevocationSyncEntityID("windows-main")
	second := deviceRevocationSyncEntityID("windows-main")
	different := deviceRevocationSyncEntityID("macbook-main")

	if first == "" {
		t.Fatalf("expected derived id")
	}
	if first != second {
		t.Fatalf("derived id is not stable: %s != %s", first, second)
	}
	if first == different {
		t.Fatalf("derived id should change when device id changes")
	}
}

func TestStringSlicePayloadAcceptsJSONAndDelimitedStrings(t *testing.T) {
	jsonItems := stringSlicePayload(map[string]any{"event_ids": []any{"a", " b ", 12, ""}}, "event_ids")
	if len(jsonItems) != 2 || jsonItems[0] != "a" || jsonItems[1] != "b" {
		t.Fatalf("unexpected json slice parse: %#v", jsonItems)
	}

	delimited := stringSlicePayload(map[string]any{"event_ids": "a, b,,c"}, "event_ids")
	if len(delimited) != 3 || delimited[0] != "a" || delimited[1] != "b" || delimited[2] != "c" {
		t.Fatalf("unexpected delimited parse: %#v", delimited)
	}
}
