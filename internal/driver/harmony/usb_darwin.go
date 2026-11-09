package harmony

import (
	"context"
	"os/exec"
	"time"

	"howett.net/plist"
)

// usbTargets reads only USB descriptors. It does not open a debugging
// connection, and matches HDC's interface instead of all Huawei products.
func usbTargets(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-a", "-l", "-r", "-c", "IOUSBHostDevice").Output()
	if err != nil {
		return nil, err
	}
	return parseUSB(out)
}

func parseUSB(out []byte) ([]string, error) {
	var nodes []usbNode
	if _, err := plist.Unmarshal(out, &nodes); err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]bool{}
	var walk func(usbNode)
	walk = func(n usbNode) {
		if n.Serial != "" && !seen[n.Serial] {
			for _, c := range n.Children {
				if c.Class == 255 && c.SubClass == 80 && c.Protocol == 1 {
					seen[n.Serial] = true
					ids = append(ids, n.Serial)
					break
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return ids, nil
}

type usbNode struct {
	Serial   string    `plist:"USB Serial Number"`
	Class    uint64    `plist:"bInterfaceClass"`
	SubClass uint64    `plist:"bInterfaceSubClass"`
	Protocol uint64    `plist:"bInterfaceProtocol"`
	Children []usbNode `plist:"IORegistryEntryChildren"`
}
