# CI coverage

Pull requests that modify only the 23 reviewed, existing browser test files can use the browser profile. The classifier runs from the event's exact base commit in a separate checkout and examines the exact tested Git tree. Both commits must retain the complete regular-file inventory, browser build constraint, external test package and reviewed first-party imports. Additions, deletions, renames, production code, assets, fixtures, dependency files, workflow changes and unknown paths use full acceptance. Manual and merge-group runs also use the full profile.

Both profiles run controller/API tests, build, chart checks, core lint, release-contract checks, the real browser suite and browser-tagged lint. The full profile additionally runs all four source clusters, native Document and Graph acceptance, PostgreSQL model acceptance and AGE image acceptance. Classifier errors or missing output retain these full-suite jobs.

The required summary reads its checker from the same trusted base. It requires an exact job inventory, successful selection and every selected job's success. Only the explicitly unselected heavy jobs may report skipped in a verified browser profile. Missing, failed, cancelled, unknown or extra results fail the summary. When the trusted base has no classifier yet, only the complete successful suite can pass.

The classifier tests check the entire browser inventory and first-party dependency closure, including packages that embed assets. Workflow regressions bind every logical job to the required summary and keep the four source matrix members. Updating the reviewed inventory or classifier therefore requires full acceptance.
