package bugsink

func Verdicts() []string {
	var names []string
	for _, v := range verdicts {
		names = append(names, v.name)
	}
	return names
}
