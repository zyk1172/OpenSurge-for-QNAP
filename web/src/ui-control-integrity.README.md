# Web control integrity regression scope

This change protects the post-redesign Web UI against regressions where controls still exist in JSX but become effectively unavailable because of scroll retention, inherited card sizing, weak contrast, or stale client state.

The high-risk surfaces are Dashboard lifecycle actions, QNAP host takeover/Tailscale controls, policy-group health actions, connectivity probe actions, device dirty/drift state, and Profile Overlay desired/applied state.
