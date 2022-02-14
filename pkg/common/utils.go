package common

import (
	"net/url"
	"strings"
)

func TablePathEncode(str string) string {
	return strings.NewReplacer(".", "%2E", "-", "%2D").Replace(url.PathEscape(str))

}
