// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package graph

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/hashicorp/terraform/internal/configs"
	"github.com/hashicorp/terraform/internal/moduletest"
	hcltest "github.com/hashicorp/terraform/internal/moduletest/hcl"
)

// TransformConfigForRun transforms the run's module configuration to include
// the providers and variables from its block and the test file.
//
// In practice, this actually just means performing some surgery on the
// available providers. We want to copy the relevant providers from the test
// file into the configuration. We also want to process the providers so they
// use variables from the file instead of variables from within the test file.
//
// TODO(liamcervante): Push provider initialisation into the test graph
// so we don't have to transform the config in this way anymore.
func TransformConfigForRun(ctx *EvalContext, run *moduletest.Run, file *moduletest.File) hcl.Diagnostics {
	var diags hcl.Diagnostics

	// Currently, we only need to override the provider settings.
	//
	// We can have a set of providers defined within the config, we can also
	// have a set of providers defined within the test file. Then the run can
	// also specify a set of overrides that tell Terraform exactly which
	// providers from the test file to apply into the config.
	//
	// The process here is as follows:
	//   1. Take all the providers in the original config keyed by name.alias,
	//      we call this `previous`
	//   2. Copy them all into a new map, we call this `next`.
	//   3a. If the run has configuration specifying provider overrides, we copy
	//       only the specified providers from the test file into `next`. While
	//       doing this we ensure to preserve the name and alias from the
	//       original config.
	//   3b. If the run has no override configuration, we copy all the providers
	//       from the test file into `next`, overriding all providers with name
	//       collisions from the original config.
	//   4. We then modify the original configuration so that the providers it
	//      holds are the combination specified by the original config, the test
	//      file and the run file.
	//   5. We then return a function that resets the original config back to
	//      its original state. This can be called by the surrounding test once
	//      completed so future run blocks can safely execute.

	// First, initialise `previous` and `next`. `previous` contains a backup of
	// the providers from the original config. `next` contains the set of
	// providers that will be used by the test. `next` starts with the set of
	// providers from the original config.
	previous := run.ModuleConfig.Module.ProviderConfigs
	next := make(map[string]*configs.Provider)
	for key, value := range previous {
		next[key] = value
	}

	runOutputs := ctx.GetOutputs()

	if len(run.Config.Providers) > 0 {
		// Then we'll only copy over and overwrite the specific providers asked
		// for by this run block.
		for _, ref := range run.Config.Providers {
			testProvider, ok := file.Config.Providers[ref.InParent.String()]
			if !ok {
				// Then this reference was invalid as we didn't have the
				// specified provider in the parent. This should have been
				// caught earlier in validation anyway so is unlikely to happen.
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  fmt.Sprintf("Missing provider definition for %s", ref.InParent.String()),
					Detail:   "This provider block references a provider definition that does not exist.",
					Subject:  ref.InParent.NameRange.Ptr(),
				})
				continue
			}

			next[ref.InChild.String()] = &configs.Provider{
				Name:       ref.InChild.Name,
				NameRange:  ref.InChild.NameRange,
				Alias:      ref.InChild.Alias,
				AliasRange: ref.InChild.AliasRange,
				Config: &hcltest.ProviderConfig{
					Original:            testProvider.Config,
					VariableCache:       ctx.VariableCache,
					AvailableRunOutputs: runOutputs,
				},
				Mock:      testProvider.Mock,
				MockData:  testProvider.MockData,
				DeclRange: testProvider.DeclRange,
			}
		}
	} else {
		// Otherwise, let's copy over and overwrite all providers specified by
		// the test file itself.
		for key, provider := range file.Config.Providers {

			if !ctx.ProviderExists(run, key) {
				// Then we don't actually need this provider for this
				// configuration, so skip it.
				continue
			}

			next[key] = &configs.Provider{
				Name:       provider.Name,
				NameRange:  provider.NameRange,
				Alias:      provider.Alias,
				AliasRange: provider.AliasRange,
				Config: &hcltest.ProviderConfig{
					Original:            provider.Config,
					VariableCache:       ctx.VariableCache,
					AvailableRunOutputs: runOutputs,
				},
				Mock:      provider.Mock,
				MockData:  provider.MockData,
				DeclRange: provider.DeclRange,
			}
		}
	}

	run.ModuleConfig.Module.ProviderConfigs = next
	return diags
}
