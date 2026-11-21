package metadata

import (
	"encoding/json"
	"strings"
)

// maxRawValue caps any one value kept in asset_metadata.raw. Large values (ComfyUI workflow JSON,
// embedded XML) are re-read from the file by the provenance parsers, which store them as blobs.
const maxRawValue = 8 << 10

// dropped tags change on every read or only describe the file system.
var dropped = map[string]bool{
	"SourceFile": true, "System:FileAccessDate": true, "System:FileInodeChangeDate": true,
	"System:Directory": true, "System:FileName": true, "System:FilePermissions": true,
	"ExifTool:ExifToolVersion": true,
}

// RawJSON returns the tags as JSON for asset_metadata.raw: housekeeping removed, huge values
// replaced by a marker, and NUL characters (which Postgres jsonb rejects) stripped.
func RawJSON(t Tags) ([]byte, error) {
	out := make(map[string]any, len(t))
	for k, v := range t {
		if dropped[k] {
			continue
		}
		out[k] = clean(v)
	}
	return json.Marshal(out)
}

func clean(v any) any {
	switch x := v.(type) {
	case string:
		if len(x) > maxRawValue {
			return map[string]any{"truncated": true, "length": len(x)}
		}
		return strings.ReplaceAll(x, "\x00", "")
	case []any:
		for i := range x {
			x[i] = clean(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = clean(x[k])
		}
		return x
	}
	return v
}
