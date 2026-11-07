package android

import "testing"

func TestParseProps(t *testing.T) {
	out := "[ro.product.brand]: [Redmi]\r\n[ro.mi.os.version.name]: [OS2.0]\n[empty]: []\n[gsm.operator.alpha]: [CMCC,]\nnot a property\n"
	props := parseProps(out)
	if props["ro.product.brand"] != "Redmi" || props["ro.mi.os.version.name"] != "OS2.0" || props["gsm.operator.alpha"] != "CMCC," {
		t.Errorf("props = %q", props)
	}
	if v, ok := props["empty"]; !ok || v != "" {
		t.Errorf("an empty property: %q, %v", v, ok)
	}
	if len(props) != 4 {
		t.Errorf("%d properties: %q", len(props), props)
	}
}

func TestFamilyOf(t *testing.T) {
	for _, c := range []struct {
		name           string
		props          map[string]string
		family, system string
	}{
		{"HyperOS 2", map[string]string{"ro.product.manufacturer": "Xiaomi", "ro.product.brand": "Redmi",
			"ro.mi.os.version.name": "OS2.0", "ro.miui.ui.version.name": "V816"}, "xiaomi", "HyperOS 2.0"},
		{"MIUI 14", map[string]string{"ro.product.manufacturer": "Xiaomi", "ro.miui.ui.version.name": "V140"}, "xiaomi", "MIUI 14"},
		{"MIUI 12.5", map[string]string{"ro.product.manufacturer": "Xiaomi", "ro.miui.ui.version.name": "V125"}, "xiaomi", "MIUI 12.5"},
		{"LineageOS on a Mi 10 Pro", map[string]string{"ro.product.manufacturer": "Xiaomi", "ro.product.brand": "Xiaomi",
			"ro.product.model": "Mi 10 Pro", "ro.lineage.build.version": "23.0"}, "", "LineageOS 23.0"},
		{"ColorOS", map[string]string{"ro.product.manufacturer": "OPPO", "ro.product.brand": "OPPO", "ro.build.version.oplusrom": "V15.0.0"}, "oppo", "ColorOS 15.0.0"},
		{"older ColorOS", map[string]string{"ro.product.manufacturer": "OPPO", "ro.build.version.opporom": "V7.1"}, "oppo", "ColorOS 7.1"},
		{"OxygenOS", map[string]string{"ro.product.manufacturer": "OnePlus", "ro.product.brand": "OnePlus", "ro.build.version.oplusrom": "V15.0.0"}, "oppo", "OxygenOS 15.0.0"},
		{"realme UI", map[string]string{"ro.product.manufacturer": "realme", "ro.product.brand": "realme",
			"ro.build.version.oplusrom": "V14.0", "ro.build.version.realmeui": "V5.0"}, "oppo", "realme UI 5.0"},
		{"OriginOS", map[string]string{"ro.product.manufacturer": "vivo", "ro.product.brand": "iQOO", "ro.vivo.os.name": "OriginOS", "ro.vivo.os.version": "5.0"}, "vivo", "OriginOS 5.0"},
		{"Funtouch OS", map[string]string{"ro.product.manufacturer": "vivo", "ro.vivo.os.name": "Funtouch", "ro.vivo.os.version": "14.0"}, "vivo", "Funtouch OS 14.0"},
		{"MagicOS", map[string]string{"ro.product.manufacturer": "HONOR", "ro.build.version.magic": "MagicOS_9.0.0"}, "honor", "MagicOS 9.0.0"},
		{"Magic UI on EMUI", map[string]string{"ro.product.manufacturer": "HONOR", "ro.build.version.emui": "EmotionUI_10.0.0"}, "honor", "EMUI 10.0.0"},
		{"HarmonyOS", map[string]string{"ro.product.manufacturer": "HUAWEI", "ro.build.version.emui": "EmotionUI_14.2.0", "hw_sc.build.platform.version": "4.2.0"}, "huawei", "HarmonyOS 4.2.0"},
		{"EMUI", map[string]string{"ro.product.manufacturer": "HUAWEI", "ro.build.version.emui": "EmotionUI_12.0.0"}, "huawei", "EMUI 12.0.0"},
		{"One UI 6.1", map[string]string{"ro.product.manufacturer": "samsung", "ro.build.version.oneui": "60100"}, "samsung", "One UI 6.1"},
		{"Samsung without the version", map[string]string{"ro.product.manufacturer": "samsung"}, "samsung", "One UI"},
		{"Pixel", map[string]string{"ro.product.manufacturer": "Google", "ro.product.model": "Pixel 9"}, "pixel", ""},
		{"emulator", map[string]string{"ro.product.manufacturer": "Google", "ro.product.model": "sdk_gphone64_arm64", "ro.kernel.qemu": "1"}, "", ""},
		{"Motorola", map[string]string{"ro.product.manufacturer": "motorola", "ro.product.model": "moto g"}, "", ""},
	} {
		family, system := familyOf(c.props)
		if family != c.family || system != c.system {
			t.Errorf("%s: %q, %q; want %q, %q", c.name, family, system, c.family, c.system)
		}
	}
}

func TestVersions(t *testing.T) {
	for in, want := range map[string]string{"V125": "12.5", "V130": "13", "V140": "14", "V12": "12", "v11": "11", "Vx": ""} {
		if got := miuiVersion(in); got != want {
			t.Errorf("miuiVersion(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"60100": "6.1", "80000": "8.0", "50101": "5.1", "x": "", "12": ""} {
		if got := oneUIVersion(in); got != want {
			t.Errorf("oneUIVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInputRefused(t *testing.T) {
	for line, want := range map[string]bool{
		"java.lang.SecurityException: Injecting input events requires the caller (or the source of the instrumentation, if any) to have the INJECT_EVENTS permission.": true,
		"[server] ERROR: java.lang.SecurityException: Injecting to another application requires INJECT_EVENTS permission":                                             true,
		"[server] INFO: Device: [Xiaomi] Redmi 23013RK75C (Android 14)":                                                                                                false,
	} {
		if got := inputRefused(line); got != want {
			t.Errorf("inputRefused(%q) = %v", line, got)
		}
	}
	if got := serverMessage("[server] ERROR: java.lang.SecurityException: no"); got != "java.lang.SecurityException: no" {
		t.Errorf("serverMessage = %q", got)
	}
}
