package api

import "strings"

func parseTagString(input string) []MetaTag {
	// Split by comma OR semicolon
	rawTags := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == ';'
	})

	var tags []MetaTag
	for _, t := range rawTags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}

		parts := strings.SplitN(t, "=", 2)
		key := strings.TrimSpace(parts[0])
		val := ""
		if len(parts) > 1 {
			val = strings.TrimSpace(parts[1])
		}

		tags = append(tags, MetaTag{
			Key:   key,
			Value: val,
		})
	}
	return tags
}
