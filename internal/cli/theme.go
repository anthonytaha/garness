package cli

import (
	"fmt"
	"strconv"
	"strings"
)

const defaultAccentColor = "82"

// Named accents map to the terminal's 256-color palette. Hex colors are
// downsampled by the renderer when the terminal cannot display true color.
func parseAccentColor(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "green":
		return defaultAccentColor, nil
	case "amber":
		return "214", nil
	case "blue":
		return "75", nil
	case "cyan":
		return "51", nil
	case "magenta":
		return "213", nil
	case "red":
		return "203", nil
	case "white":
		return "15", nil
	}
	if len(value) == 7 && value[0] == '#' {
		if _, err := strconv.ParseUint(value[1:], 16, 24); err == nil {
			return value, nil
		}
	}
	if value != "" && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		if index, err := strconv.Atoi(value); err == nil && index <= 255 {
			return strconv.Itoa(index), nil
		}
	}
	return "", fmt.Errorf("accent color must be green, amber, blue, cyan, magenta, red, white, #RRGGBB, or a palette index from 0 to 255")
}
