<!-- agents:begin evidence@667078 -->
# Working with evidence

Principles, not procedures. Each was paid for at least once; the incidents are deliberately left out — a rule you accept on the strength of its anecdote is a rule you have not understood. Read this before measuring anything, verifying a claim, or writing down a cause.

## What a number means

- **Absence of a row is not an observation.** Distinguish "observed X", "observed not-X", and "nothing was recorded" every time. Collapsing the last two has shipped repeatedly.
- **Before quoting a zero, ask whether the thing counted could ever have been non-zero.** A definitional zero reads as "it costs nothing".
- **Calibrate a new measurement against a known answer before trusting anything else it says**, and freeze that target — one the codebase can repair out from under you is a countdown, not a calibration.
- **Move one variable at a time**, or the result is uninterpretable and gets misread in whichever direction someone already believed.

## What counts as verified

- **Verify the inference, not just the facts.** True cited facts are not a true conclusion. Ask what else could be true — above all whether the function you cite is the *only* source of the fact. The tell is a plural noun doing quiet work ("the handlers", "the gates"): ask *which one*.
- **Reading the source is not running it.** A claim verified only by reading code is a hypothesis.
- **One plausible cause is not one cause.** Necessary is not sufficient; re-measure after the fix rather than declaring victory on the mechanism.
- **When a user says nothing happened, the cheapest hypothesis is that nothing happened.** "It works, it's just hard to find" is itself a claim about behaviour and needs the same evidence.
- **Prove a guard by mutation**: remove it, show the specific breakage with a number, put it back. "The COALESCE is needed" is an assertion; "dropping it inserts 386 duplicates" is a measurement.
- **Inspecting a generated artifact's source is not inspecting its output** — render the SVG/PDF/chart and look at it.

## Before you build it

- **It probably already exists** — the feature, the tool, the answer. Checking costs one command (`ls scripts/`). A feature nobody can find is a discoverability defect, which is a different fix from rebuilding it.
- **A constant you remember is not a constant you checked.** Validate any figure, equation or spec against a source before committing it, and record where it came from next to the value.
- **Choose tolerance or loud failure by consequence, never silence.** Tolerate where a silent empty would cause an un-undoable write; fail loudly where a mismatch means a vendor changed something you want to hear about. Tolerance must announce itself.
- **An illustration is not a designation.** "E.g." in a requirement is an example, not the spec.
<!-- agents:end evidence -->
