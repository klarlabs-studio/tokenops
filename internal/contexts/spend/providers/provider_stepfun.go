package providers

func providerStepFun() Descriptor {
	return Descriptor{
		ID:          "stepfun",
		DisplayName: "StepFun",
		Logo:        true,
		// Ported from CodexBar's StepFun provider
		// (Sources/CodexBarCore/Providers/StepFun/StepFunUsageFetcher.swift,
		// docs/stepfun.md). Setup signs in once with the username and
		// password and stores only the Oasis-Token; STEPFUN_USERNAME and
		// STEPFUN_PASSWORD are not read, since the daemon never holds a
		// password.
		Sources: []Source{
			{Name: "stepfun_account", Tag: "stepfun-account", Kind: Subscription, Credential: PasswordLogin,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`POST /api/step.openapi.devcenter.Dashboard/QueryStepPlanRateLimit` (platform.stepfun.com)",
				Shows:    "a Coding Plan's 5-hour and weekly windows, or a Token Plan's credit pool"},
		},
		EnvVars: []string{"STEPFUN_TOKEN"},
		Docs:    Docs{Label: "StepFun Step Plan"},
	}
}
