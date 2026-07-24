package main

import "strings"

func windowsProductName(productName string, major, minor uint32) string {
	if productName = strings.TrimSpace(productName); productName != "" {
		return productName
	}
	switch {
	case major == 5 && minor == 2:
		return "Windows Server 2003"
	case major == 6 && minor == 0:
		return "Windows Server 2008"
	case major == 6 && minor == 1:
		return "Windows Server 2008 R2"
	case major == 6 && minor == 2:
		return "Windows Server 2012"
	case major == 6 && minor == 3:
		return "Windows Server 2012 R2"
	default:
		return "Windows"
	}
}
