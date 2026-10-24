package modelrepo

import "strings"

const ModelResourcePrefix = "models/"

func ModelID(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), ModelResourcePrefix)
}

func SameModel(a, b string) bool {
	return ModelID(a) == ModelID(b)
}
