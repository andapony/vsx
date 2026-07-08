# Two extraction postures: best-effort by default, strict as a pass/fail gate

`vsx` runs **best-effort** by default: it reports every departure from the spec
(a *deviation*), continues past missing or ambiguous data with a documented
guess/default, writes all recoverable audio, and **exits non-zero if any
deviation occurred** (the WAVs are still written — the non-zero exit only
signals that the result is suspect, so the tool is honest in a pipeline without
ever withholding recoverable audio). Only genuinely unidentifiable or unreadable
input is a hard error; a recognized-but-suspect input (a cooked/`dd` CD rip, a
dump missing its trailing TDI filler) is attempted with a prominent warning, not
refused.

`--strict` inverts this into a **conformance gate**: the first deviation
anywhere aborts the whole run with no output. Strict answers a different
question from extraction — "is this rip a perfect, spec-clean image?" — for
which a partial WAV dump would only muddy the pass/fail verdict.

## Consequences

- Two use cases are served without compromise: *get whatever audio exists*
  (best-effort) and *validate an image is clean* (strict). Withholding output
  under best-effort, or emitting partial output under strict, would each defeat
  one of them.
- Exit codes carry meaning: `0` = clean, non-zero = deviations occurred (or, in
  strict mode, aborted). Callers can gate on this.
