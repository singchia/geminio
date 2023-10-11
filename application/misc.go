package application

func strings2interfaces(strs ...string) []interface{} {
	ifs := make([]interface{}, len(strs), len(strs))
	for index, str := range strs {
		ifs[index] = str
	}
	return ifs
}
