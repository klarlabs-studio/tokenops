package accounts

import (
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// readerAlibabaTokenPlanCLI registers the reader (readers_gen.go): `bl
// usage token-plan --output json`, Alibaba Cloud's Bailian CLI, run as its
// descriptor says (cliSource) with only the environment it needs, as
// CodexBar's Alibaba Token Plan provider runs it
// (AlibabaTokenPlanCLIUsageFetcher.swift). Its answer carries the same
// rolling windows as the console's, parsed the same way.
func readerAlibabaTokenPlanCLI() usage.Reader {
	return cliSource{provider: "alibabatokenplan", tag: "alibabatokenplan-cli", parse: parseTokenPlanCLI}
}

func parseTokenPlanCLI(out string) (usage.Reading, error) {
	return parseTokenPlanWindows([]byte(jsonPart(out)))
}
