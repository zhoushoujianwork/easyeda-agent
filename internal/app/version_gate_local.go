package app

func evaluateLocalRuntime(cli, daemon string, connectors []string) versionGateReport {
	r := versionGateReport{CLI: cli, Daemon: daemon, Connectors: connectors}
	check := func(name, actual string) {
		f := versionFinding{Component: name, Version: actual, Severity: versionSevOK, Reason: "exact local development version"}
		if normVersion(actual) != normVersion(cli) {
			f.Severity = versionSevBlock
			f.Reason = "local development requires exact version including dev suffix"
		}
		r.Findings = append(r.Findings, f)
	}
	check("daemon", daemon)
	if len(connectors) == 0 {
		check("connector", "")
	}
	for _, c := range connectors {
		check("connector", c)
	}
	r.Verdict = worstSeverity(r.Findings)
	return r
}
