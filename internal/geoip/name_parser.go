package geoip

import "strings"

// regionRule defines keywords for matching a region from proxy node names.
type regionRule struct {
	region string
	emojis []string // Flag emoji patterns (exact substring match)
	names  []string // Chinese/English keywords (case-insensitive contains)
	codes  []string // Short codes like "HK", "US" (word-boundary matched)
}

// regionRules defines the keyword matching rules for each region.
// Order matters: more specific matches (e.g., "印度尼西亚") must come before
// shorter overlapping ones (e.g., "印度") to avoid false positives.
var regionRules = []regionRule{
	{
		region: RegionHK,
		emojis: []string{"🇭🇰"},
		names:  []string{"香港", "hong kong", "hongkong"},
		codes:  []string{"HK"},
	},
	{
		region: RegionTW,
		emojis: []string{"🇹🇼"},
		names:  []string{"台湾", "台北", "台中", "taiwan", "taipei"},
		codes:  []string{"TW"},
	},
	{
		region: RegionJP,
		emojis: []string{"🇯🇵"},
		names:  []string{"日本", "东京", "大阪", "japan", "tokyo", "osaka"},
		codes:  []string{"JP"},
	},
	{
		region: RegionKR,
		emojis: []string{"🇰🇷"},
		names:  []string{"韩国", "首尔", "korea", "seoul"},
		codes:  []string{"KR"},
	},
	{
		region: RegionUS,
		emojis: []string{"🇺🇸"},
		names: []string{
			"美国", "洛杉矶", "纽约", "旧金山", "西雅图", "芝加哥",
			"达拉斯", "圣何塞", "硅谷", "凤凰城",
			"united states", "america", "los angeles", "new york",
			"san francisco", "seattle", "chicago", "dallas",
			"silicon valley", "san jose", "phoenix",
		},
		codes: []string{"US", "USA"},
	},
	{
		region: RegionSG,
		emojis: []string{"🇸🇬"},
		names:  []string{"新加坡", "狮城", "singapore"},
		codes:  []string{"SG"},
	},
	{
		region: RegionGB,
		emojis: []string{"🇬🇧"},
		names:  []string{"英国", "伦敦", "united kingdom", "britain", "england", "london"},
		codes:  []string{"UK", "GB"},
	},
	{
		region: RegionDE,
		emojis: []string{"🇩🇪"},
		names:  []string{"德国", "法兰克福", "柏林", "germany", "frankfurt", "berlin"},
		codes:  []string{"DE"},
	},
	{
		region: RegionFR,
		emojis: []string{"🇫🇷"},
		names:  []string{"法国", "巴黎", "france", "paris"},
		codes:  []string{"FR"},
	},
	{
		region: RegionNL,
		emojis: []string{"🇳🇱"},
		names:  []string{"荷兰", "阿姆斯特丹", "netherlands", "holland", "amsterdam"},
		codes:  []string{"NL"},
	},
	{
		region: RegionCA,
		emojis: []string{"🇨🇦"},
		names:  []string{"加拿大", "多伦多", "温哥华", "canada", "toronto", "vancouver"},
		codes:  []string{"CA"},
	},
	{
		region: RegionAU,
		emojis: []string{"🇦🇺"},
		names:  []string{"澳大利亚", "澳洲", "悉尼", "墨尔本", "australia", "sydney", "melbourne"},
		codes:  []string{"AU"},
	},
	{
		// Indonesia must come before India to avoid "印度尼西亚" matching "印度"
		region: RegionPH,
		emojis: []string{"🇵🇭"},
		names:  []string{"菲律宾", "马尼拉", "philippines", "manila"},
		codes:  []string{"PH"},
	},
	{
		region: RegionIN,
		emojis: []string{"🇮🇳"},
		names:  []string{"印度", "孟买", "india", "mumbai"},
		// "IN" is too ambiguous (common English word), skip code matching
		codes: nil,
	},
	{
		region: RegionRU,
		emojis: []string{"🇷🇺"},
		names:  []string{"俄罗斯", "莫斯科", "russia", "moscow"},
		codes:  []string{"RU"},
	},
	{
		region: RegionTR,
		emojis: []string{"🇹🇷"},
		names:  []string{"土耳其", "伊斯坦布尔", "turkey", "istanbul"},
		codes:  []string{"TR"},
	},
	{
		region: RegionTH,
		emojis: []string{"🇹🇭"},
		names:  []string{"泰国", "曼谷", "thailand", "bangkok"},
		codes:  []string{"TH"},
	},
}

// RegionFromName attempts to determine the region from a proxy node name
// by matching against known keywords (emoji flags, Chinese names, English names, short codes).
// Returns RegionOther if no match is found.
func RegionFromName(name string) string {
	if name == "" {
		return RegionOther
	}

	nameLower := strings.ToLower(name)

	for _, rule := range regionRules {
		if matchRule(name, nameLower, rule) {
			return rule.region
		}
	}

	return RegionOther
}

func matchRule(name, nameLower string, rule regionRule) bool {
	// 1. Check emoji flags (exact substring in original name)
	for _, emoji := range rule.emojis {
		if strings.Contains(name, emoji) {
			return true
		}
	}

	// 2. Check Chinese/English keywords (case-insensitive)
	for _, kw := range rule.names {
		if strings.Contains(nameLower, kw) {
			return true
		}
	}

	// 3. Check short codes with word boundaries
	for _, code := range rule.codes {
		if containsCode(name, code) {
			return true
		}
	}

	return false
}

// containsCode checks if the short code appears in the name at word boundaries.
// A word boundary means the character before/after the code is not an ASCII letter.
// Digits and other characters (including Chinese) are treated as boundaries.
func containsCode(name, code string) bool {
	nameUpper := strings.ToUpper(name)
	codeUpper := strings.ToUpper(code)
	start := 0
	for {
		idx := strings.Index(nameUpper[start:], codeUpper)
		if idx == -1 {
			return false
		}
		idx += start

		beforeOK := idx == 0 || !isASCIILetter(nameUpper[idx-1])
		afterIdx := idx + len(codeUpper)
		afterOK := afterIdx >= len(nameUpper) || !isASCIILetter(nameUpper[afterIdx])

		if beforeOK && afterOK {
			return true
		}
		start = idx + 1
	}
}

func isASCIILetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
