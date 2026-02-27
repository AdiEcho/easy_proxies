package geoip

import "testing"

func TestRegionFromName(t *testing.T) {
	tests := []struct {
		name   string
		expect string
	}{
		// Emoji flags
		{"🇭🇰 香港 01 | IPLC", RegionHK},
		{"🇯🇵 Tokyo Premium", RegionJP},
		{"🇺🇸 US-01", RegionUS},
		{"🇸🇬 Singapore", RegionSG},
		{"🇬🇧 London", RegionGB},
		{"🇩🇪 Frankfurt", RegionDE},
		{"🇹🇼 台北 01", RegionTW},
		{"🇰🇷 Seoul", RegionKR},
		{"🇫🇷 Paris", RegionFR},
		{"🇳🇱 Amsterdam", RegionNL},
		{"🇨🇦 Toronto", RegionCA},
		{"🇦🇺 Sydney", RegionAU},
		{"🇮🇳 Mumbai", RegionIN},
		{"🇷🇺 Moscow", RegionRU},
		{"🇹🇷 Istanbul", RegionTR},
		{"🇹🇭 Bangkok", RegionTH},
		{"🇵🇭 Manila", RegionPH},

		// Chinese keywords
		{"香港01-IPLC", RegionHK},
		{"日本东京01", RegionJP},
		{"韩国首尔Premium", RegionKR},
		{"美国洛杉矶01", RegionUS},
		{"台湾台北01", RegionTW},
		{"新加坡01", RegionSG},
		{"英国伦敦01", RegionGB},
		{"德国法兰克福01", RegionDE},
		{"法国巴黎01", RegionFR},
		{"荷兰阿姆斯特丹01", RegionNL},
		{"加拿大温哥华01", RegionCA},
		{"澳大利亚悉尼01", RegionAU},
		{"澳洲墨尔本01", RegionAU},
		{"印度孟买01", RegionIN},
		{"俄罗斯莫斯科01", RegionRU},
		{"土耳其伊斯坦布尔01", RegionTR},
		{"泰国曼谷01", RegionTH},
		{"菲律宾马尼拉01", RegionPH},

		// English names (case-insensitive)
		{"Japan Tokyo 01", RegionJP},
		{"korea-seoul-premium", RegionKR},
		{"los angeles premium", RegionUS},
		{"Hong Kong 01", RegionHK},
		{"taiwan-01", RegionTW},
		{"singapore premium", RegionSG},
		{"united kingdom 01", RegionGB},
		{"germany frankfurt", RegionDE},
		{"france-01", RegionFR},
		{"netherlands-01", RegionNL},
		{"canada vancouver", RegionCA},
		{"australia sydney", RegionAU},
		{"india mumbai", RegionIN},
		{"russia moscow", RegionRU},
		{"turkey istanbul", RegionTR},
		{"thailand bangkok", RegionTH},
		{"philippines manila", RegionPH},

		// Short codes with word boundaries
		{"HK-01 Premium", RegionHK},
		{"JP-Tokyo-01", RegionJP},
		{"KR01", RegionKR},
		{"US 01", RegionUS},
		{"USA-Premium", RegionUS},
		{"TW-Premium", RegionTW},
		{"SG-Premium", RegionSG},
		{"UK-London", RegionGB},
		{"GB-01", RegionGB},
		{"DE-Frankfurt", RegionDE},
		{"FR-Paris", RegionFR},
		{"NL-01", RegionNL},
		{"CA-Toronto", RegionCA},
		{"AU-Sydney", RegionAU},
		{"RU-Moscow", RegionRU},
		{"TR-Istanbul", RegionTR},
		{"TH-Bangkok", RegionTH},
		{"PH-Manila", RegionPH},

		// Code should NOT match inside words
		{"CHECK-01", RegionOther},
		{"THRUST-01", RegionOther},

		// No match
		{"节点01", RegionOther},
		{"Premium Node", RegionOther},
		{"", RegionOther},

		// Mixed patterns
		{"v2-🇭🇰香港IPLC01", RegionHK},
		{"Premium HK 高速", RegionHK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RegionFromName(tt.name)
			if got != tt.expect {
				t.Errorf("RegionFromName(%q) = %q, want %q", tt.name, got, tt.expect)
			}
		})
	}
}

func TestContainsCode(t *testing.T) {
	tests := []struct {
		name   string
		code   string
		expect bool
	}{
		{"HK-01", "HK", true},
		{"hk-01", "HK", true},
		{"HK01", "HK", true},   // digit is a valid boundary
		{"CHECK", "HK", false}, // H is preceded by letter C
		{"THKU", "HK", false},  // H preceded by T, K followed by U
		{"US Premium", "US", true},
		{"THRUST", "US", false}, // U preceded by letter
		{"SG", "SG", true},      // exact match
		{" SG ", "SG", true},
		{"MSG01", "SG", false}, // S preceded by M
		{"USA-01", "USA", true},
		{"US-01", "USA", false}, // only "US" not "USA"
	}

	for _, tt := range tests {
		t.Run(tt.name+"_"+tt.code, func(t *testing.T) {
			got := containsCode(tt.name, tt.code)
			if got != tt.expect {
				t.Errorf("containsCode(%q, %q) = %v, want %v", tt.name, tt.code, got, tt.expect)
			}
		})
	}
}
