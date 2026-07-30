package handlers

import (
	"fmt"
	"strings"
)

// normalizeHongKongPhone canonicalizes a Hong Kong contact number. Delivery
// contacts may be fixed or mobile, so all 2-9 prefixes are accepted.
func normalizeHongKongPhone(raw string) (string, error) {
	local, err := hongKongLocalPhoneDigits(raw)
	if err != nil {
		return "", err
	}
	if local[0] < '2' || local[0] > '9' {
		return "", fmt.Errorf("Hong Kong phone number must start with 2-9")
	}
	return "+852" + local, nil
}

// normalizeHongKongMobilePhone canonicalizes an account mobile number. Hong
// Kong mobile services use the newer 4/7/8 prefixes as well as 5/6/9.
func normalizeHongKongMobilePhone(raw string) (string, error) {
	local, err := hongKongLocalPhoneDigits(raw)
	if err != nil {
		return "", err
	}
	if local[0] < '4' || local[0] > '9' {
		return "", fmt.Errorf("Hong Kong mobile number must start with 4-9")
	}
	return "+852" + local, nil
}

func hongKongLocalPhoneDigits(raw string) (string, error) {
	phone := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(raw))
	phone = strings.TrimPrefix(phone, "+852")
	if strings.HasPrefix(phone, "852") && len(phone) == 11 {
		phone = phone[3:]
	}
	if len(phone) != 8 {
		return "", fmt.Errorf("Hong Kong phone number must contain 8 digits")
	}
	for _, char := range phone {
		if char < '0' || char > '9' {
			return "", fmt.Errorf("Hong Kong phone number must contain 8 digits")
		}
	}
	return phone, nil
}
