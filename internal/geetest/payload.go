package geetest

import "strings"

func buildGetCSPayload(gt, challenge string, getTypeData object) object {
	o := obj(
		"gt", gt,
		"challenge", challenge,
		"offline", false,
		"new_captcha", true,
		"product", "embed",
		"width", "300px",
		"https", true,
		"protocol", "https://",
	)
	for _, m := range getTypeData {
		o.set(m.key, m.value)
	}
	o.set("cc", 16)
	o.set("ww", true)
	o.set("i", strings.Repeat("-1!!", 70)+"-1")
	return o
}

func ajaxMousePath() []mouseEvent {
	return []mouseEvent{
		{kind: "move", x: 385, y: 313, timestamp: 1724572150164, dom: "pointermove", fields: 5},
		{kind: "move", x: 385, y: 315, timestamp: 1724572150166, dom: "pointermove", fields: 5},
		{kind: "move", x: 386, y: 315, timestamp: 1724572150174, dom: "pointermove", fields: 5},
		{kind: "move", x: 387, y: 315, timestamp: 1724572150182, dom: "pointermove", fields: 5},
		{kind: "move", x: 387, y: 316, timestamp: 1724572150188, dom: "pointermove", fields: 5},
		{kind: "move", x: 388, y: 316, timestamp: 1724572150204, dom: "pointermove", fields: 5},
		{kind: "move", x: 388, y: 317, timestamp: 1724572150218, dom: "pointermove", fields: 5},
		{kind: "down", x: 388, y: 317, timestamp: 1724572150586, dom: "pointerdown", fields: 5},
		{kind: "focus", timestamp: 1724572150587, fields: 2},
		{kind: "up", x: 388, y: 317, timestamp: 1724572150632, dom: "pointerup", fields: 5},
	}
}

func buildAjaxPayload(tt, rp string) object {
	return obj(
		"lang", "zh-cn",
		"type", "fullpage",
		"tt", tt,
		"light", "DIV_0",
		"s", "c7c3e21112fe4f741921cb3e4ff9f7cb",
		"h", "321f9af1e098233dbd03f250fd2b5e21",
		"hh", "39bd9cad9e425c3a8f51610fd506e3b3",
		"hi", "09eb21b3ae9542a9bc1e8b63b3d9a467",
		"vip_order", -1,
		"ct", -1,
		"ep", obj(
			"v", "9.2.0-guwyxh",
			"te", false,
			"me", true,
			"ven", "Google Inc. (Intel)",
			"ren", "ANGLE (Intel, Intel(R) Iris(R) Xe Graphics (0x0000A7A0) Direct3D11 vs_5_0 ps_5_0, D3D11)",
			"fp", []any{"scroll", 0, 1602, int64(1724571628498), nil},
			"lp", []any{"up", 386, 217, int64(1724571629854), "pointerup"},
			"em", obj(
				"ph", 0,
				"cp", 0,
				"ek", "11",
				"wd", 1,
				"nt", 0,
				"si", 0,
				"sc", 0,
			),
			"tm", obj(
				"a", int64(1724571567311),
				"b", int64(1724571567549),
				"c", int64(1724571567562),
				"d", 0,
				"e", 0,
				"f", int64(1724571567312),
				"g", int64(1724571567312),
				"h", int64(1724571567312),
				"i", int64(1724571567317),
				"j", int64(1724571567423),
				"k", int64(1724571567330),
				"l", int64(1724571567423),
				"m", int64(1724571567545),
				"n", int64(1724571567547),
				"o", int64(1724571567569),
				"p", int64(1724571568259),
				"q", int64(1724571568259),
				"r", int64(1724571568261),
				"s", int64(1724571570378),
				"t", int64(1724571570378),
				"u", int64(1724571570380),
			),
			"dnf", "dnf",
			"by", 0,
		),
		"passtime", 1600,
		"rp", rp,
		"captcha_token", "2064329542",
		"tsfq", "xovrayel",
	)
}

func buildVerifyPayload(gt, challenge, a, pic, tt string) object {
	return obj(
		"lang", "zh-cn",
		"passtime", 1600,
		"a", a,
		"pic", pic,
		"tt", tt,
		"ep", obj(
			"ca", []any{
				obj("x", 524, "y", 209, "t", 0, "dt", 1819),
				obj("x", 558, "y", 299, "t", 0, "dt", 428),
				obj("x", 563, "y", 95, "t", 0, "dt", 952),
				obj("x", 670, "y", 407, "t", 3, "dt", 892),
			},
			"v", "3.1.0",
			"$_FB", false,
			"me", true,
			"tm", obj(
				"a", int64(1724585496403),
				"b", int64(1724585496605),
				"c", int64(1724585496613),
				"d", 0,
				"e", 0,
				"f", int64(1724585496404),
				"g", int64(1724585496404),
				"h", int64(1724585496404),
				"i", int64(1724585496404),
				"j", int64(1724585496404),
				"k", 0,
				"l", int64(1724585496413),
				"m", int64(1724585496601),
				"n", int64(1724585496603),
				"o", int64(1724585496618),
				"p", int64(1724585496749),
				"q", int64(1724585496749),
				"r", int64(1724585496751),
				"s", int64(1724585498068),
				"t", int64(1724585498068),
				"u", int64(1724585498069),
			),
		),
		"h9s9", "1816378497",
		"rp", md5Hex(gt+challenge+"1600"),
	)
}
