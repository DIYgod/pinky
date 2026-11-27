package harmony

import (
	"reflect"
	"testing"

	"howett.net/plist"
)

func TestUSBMatchesInterfaceNotBrand(t *testing.T) {
	iface := usbNode{Class: 255, SubClass: 80, Protocol: 1}
	nodes := []usbNode{
		{Serial: "native-harmony", Children: []usbNode{iface}},
		{Serial: "android-huawei", Children: []usbNode{{Class: 255, SubClass: 66, Protocol: 1}}},
		{Serial: "unrelated", Children: []usbNode{{Class: 255, SubClass: 80, Protocol: 2}}},
		{Children: []usbNode{{Serial: "native-harmony", Children: []usbNode{iface}}, {Serial: "second", Children: []usbNode{iface}}}},
	}
	out, err := plist.Marshal(nodes, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseUSB(out)
	if err != nil || !reflect.DeepEqual(got, []string{"native-harmony", "second"}) {
		t.Fatalf("targets = %q, %v", got, err)
	}
	if _, err := parseUSB([]byte("not a plist")); err == nil {
		t.Fatal("invalid plist accepted")
	}
}
