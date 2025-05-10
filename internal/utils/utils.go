package utils

import "fmt"

func FormatServiceURL(domain string, nodePort int32) string {
	return fmt.Sprintf("%s:%d", domain, nodePort)
}
