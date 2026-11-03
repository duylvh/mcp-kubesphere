package tools

import (
	"encoding/json"
	"errors"
	"sort"
)

var errSecretListShape = errors.New("KubeSphere returned an unexpected secret list; it is withheld because it may contain secret values")

// secretAnnotations are the only annotations kept: KubeSphere's own display
// fields. Others are dropped because tools such as kubectl, Rancher or kapp
// copy the whole object, values included, into annotations.
var secretAnnotations = []string{"kubesphere.io/alias-name", "kubesphere.io/creator", "kubesphere.io/description"}

var secretMetadata = []string{"name", "namespace", "uid", "resourceVersion", "creationTimestamp", "labels"}

// redactSecretList rebuilds a secret list from an allowlist of fields, so no
// secret value can pass through: each item keeps its identity, type, labels,
// KubeSphere display annotations and dataKeys, the sorted names of its data
// and stringData keys. A body of any other shape is withheld.
func redactSecretList(body []byte) ([]byte, error) {
	var list map[string]json.RawMessage
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, errSecretListShape
	}
	var items []map[string]any
	if raw, ok := list["items"]; !ok || string(raw) != "null" && json.Unmarshal(raw, &items) != nil {
		return nil, errSecretListShape
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		redacted, err := redactSecret(item)
		if err != nil {
			return nil, err
		}
		out = append(out, redacted)
	}
	result := map[string]any{"items": out}
	if items == nil {
		result["items"] = nil
	}
	for _, field := range []string{"totalItems", "kind", "apiVersion"} {
		if raw, ok := list[field]; ok {
			result[field] = raw
		}
	}
	return json.Marshal(result)
}

func redactSecret(item map[string]any) (map[string]any, error) {
	keys := map[string]bool{}
	for _, field := range []string{"data", "stringData"} {
		v, present := item[field]
		if !present || v == nil {
			continue
		}
		values, ok := v.(map[string]any)
		if !ok {
			return nil, errSecretListShape
		}
		for k := range values {
			keys[k] = true
		}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)

	out := map[string]any{"dataKeys": names}
	for _, field := range []string{"apiVersion", "kind", "type"} {
		if v, ok := item[field].(string); ok {
			out[field] = v
		}
	}
	metadata := map[string]any{}
	if in, ok := item["metadata"].(map[string]any); ok {
		for _, field := range secretMetadata {
			if v, ok := in[field]; ok {
				metadata[field] = v
			}
		}
		if annotations, ok := in["annotations"].(map[string]any); ok {
			kept := map[string]any{}
			for _, key := range secretAnnotations {
				if v, ok := annotations[key].(string); ok {
					kept[key] = v
				}
			}
			if len(kept) > 0 {
				metadata["annotations"] = kept
			}
		}
	}
	out["metadata"] = metadata
	return out, nil
}
