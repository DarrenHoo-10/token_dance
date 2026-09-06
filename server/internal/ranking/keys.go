package ranking

import "fmt"

var Windows = []string{"today", "7d", "30d", "all"}

func ValidWindow(window string) bool {
	switch window {
	case "today", "7d", "30d", "all":
		return true
	default:
		return false
	}
}

func WindowPrefix(window string) string {
	return "{lb:global:" + window + "}"
}

type generationKeys struct {
	All      string
	Users    string
	Versions string
	Meta     string
}

type windowKeys struct {
	Prefix     string
	Current    string
	Dirty      string
	PubSeq     string
	HotCurrent string
}

func keysForWindow(window string) windowKeys {
	prefix := WindowPrefix(window)
	return windowKeys{
		Prefix:     prefix,
		Current:    prefix + ":current",
		Dirty:      prefix + ":dirty",
		PubSeq:     prefix + ":pubseq",
		HotCurrent: prefix + ":hot:current",
	}
}

func genKeys(window, generation string) generationKeys {
	prefix := WindowPrefix(window) + ":g:" + generation
	return generationKeys{
		All:      prefix + ":all",
		Users:    prefix + ":users",
		Versions: prefix + ":versions",
		Meta:     prefix + ":meta",
	}
}

func hotRowsKey(window, snapshotID string) string {
	return fmt.Sprintf("%s:hot:%s:rows", WindowPrefix(window), snapshotID)
}

func hotMetaKey(window, snapshotID string) string {
	return fmt.Sprintf("%s:hot:%s:meta", WindowPrefix(window), snapshotID)
}
