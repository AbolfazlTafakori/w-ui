# Nodes

Other W-UI panels, watched from this one over the same API this one serves — not a purpose-built agent. Issue a token on the far panel (Nodes → tokens), add it here by address, and this panel asks it every thirty seconds: version, uptime, load, customers, whether its own limits are enforced.

An unreachable node says **which kind** of unreachable: refused, unanswered, wrong credentials, or something that answered but is not a panel.

Tokens are 256 bits of randomness, shown once, stored only as a hash. `wui token issue --name NAME` mints one from the shell.
