import { z } from 'zod';

// The site's CSP forbids eval. Schemas are built while modules load, so this module is imported
// first: otherwise zod probes `Function('')` and the browser logs a CSP violation.
z.config({ jitless: true });
