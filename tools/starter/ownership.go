package main

import (
	"fmt"
	"slices"
)

func ownershipDrift(root, base string) ([]string, error) {
	tags := starterTags(root)
	var problems []string
	for _, name := range []string{"api/openapi.yaml", "api/admin.yaml"} {
		before, err := read(root, base, name)
		if err != nil {
			return nil, err
		}
		after, err := read(root, "", name)
		if err != nil {
			return nil, err
		}
		beforeOps, afterOps := starterOperations(before, tags), starterOperations(after, tags)
		keys := map[string]bool{}
		for key := range beforeOps {
			keys[key] = true
		}
		for key := range afterOps {
			keys[key] = true
		}
		var sortedKeys []string
		for key := range keys {
			sortedKeys = append(sortedKeys, key)
		}
		slices.Sort(sortedKeys)
		for _, key := range sortedKeys {
			if beforeOps[key] != afterOps[key] {
				problems = append(problems, fmt.Sprintf("%s: starter operation changed: %s", name, key))
			}
		}
		beforeSchemas, afterSchemas := schemas(before), schemas(after)
		refs := map[string]bool{}
		for _, block := range beforeOps {
			for _, match := range referenced(block) {
				refs[match[1]] = true
			}
		}
		for _, block := range afterOps {
			for _, match := range referenced(block) {
				refs[match[1]] = true
			}
		}
		allRefs := map[string]bool{}
		for schemaName := range refs {
			for ref := range schemaClosure([]string{schemaName}, beforeSchemas) {
				allRefs[ref] = true
			}
			for ref := range schemaClosure([]string{schemaName}, afterSchemas) {
				allRefs[ref] = true
			}
		}
		sortedRefs := mapsKeys(allRefs)
		slices.Sort(sortedRefs)
		for _, schemaName := range sortedRefs {
			if beforeSchemas[schemaName] != afterSchemas[schemaName] {
				problems = append(problems, fmt.Sprintf("%s: starter schema changed: %s", name, schemaName))
			}
		}
	}
	return problems, nil
}
