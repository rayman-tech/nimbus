package utils

import (
	"fmt"
	"strings"
)

func FormatServiceURL(domain string, nodePort int32) string {
	return fmt.Sprintf("%s:%d", domain, nodePort)
}

func GetSanitizedNamespace(namespace, branch string) string {
	sanitizedNamespace := strings.ToLower(namespace)
	replacer := strings.NewReplacer(
		"/", "-",
		"_", "-",
		" ", "-",
		"#", "",
		"!", "",
		"@", "",
		".", "",
	)
	if branch != "main" && branch != "master" {
		sanitizedNamespace = fmt.Sprintf("%s-%s", namespace, replacer.Replace(branch))
	}
	return sanitizedNamespace
}
