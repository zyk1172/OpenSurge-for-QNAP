# Ownership boundary

Cloudflare optimizer does not rewrite the imported source or the user's global Profile Overlay. Its generated Host and real-IP entries exist only in the materialized effective Mihomo profile. This ensures deleting an optimizer target restores the underlying user-owned configuration on the next reconciliation.
