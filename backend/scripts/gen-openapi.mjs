// Generates api/openapi.json from the routes the router actually registers.
//
// Run it after adding or renaming a route:
//   go run ./scripts/routes > /tmp/routes.json && node scripts/gen-openapi.mjs /tmp/routes.json
//
// The route list is extracted from api/server.go by ParseRegisteredRoutes, so a
// path can never be in the specification without being served, and
// TestOpenAPICoversEveryRoute fails when a served path is missing here.

import { readFileSync, writeFileSync } from 'node:fs';

const routes = JSON.parse(readFileSync(process.argv[2], 'utf8'));

// ----- Descriptions written by hand for the resources that carry a contract.
// Everything else gets a summary derived from its handler name, which is enough
// for discovery and never lies about what is served.
const described = {
  'GET /api/v1/agents/latest':
    'Version, sha256 and pinnable download URL of the agent binaries on offer.',
  'GET /api/v1/agents/download/{os}/{arch}':
    'Download the agent binary. Answers 304 to a conditional request (ETag is the sha256).',
  'GET /api/v1/agents/download/{version}/{os}/{arch}':
    'Download a pinned version. 404 when that version is no longer the one on offer.',
  'GET /api/v1/agents/download/{os}/{arch}/sha256':
    'The sha256 of one binary, in the shasum format.',
  'PUT /api/v1/organizations/{org_slug}/incidents/{id}/status':
    'Move an incident. Acknowledging is idempotent and accepts a note and a machine actor.',
  'PUT /api/v1/organizations/{org_slug}/incidents/dedup/{key}/status':
    'Move the incident named by a webhook dedup_key, so a receiver stores no ids.',
  'GET /api/v1/organizations/{org_slug}/notification-channels/{id}/deliveries':
    'Recent webhook deliveries of one channel, with status code, attempts and response body.',
  'POST /api/v1/organizations/{org_slug}/notification-channels/{id}/deliveries/{deliveryID}/replay':
    'Re-send a recorded delivery, re-signed with a fresh timestamp.',
  'GET /api/v1/organizations/{org_slug}/hosts/{id}/agent-config':
    'The scrape fragment the agent on this host fetches for itself.',
  'PUT /api/v1/organizations/{org_slug}/hosts/{id}/agent-config':
    'Set that fragment. It is parsed the way the agent will, so a broken one is refused here.',
  'GET /api/v1/agents/config':
    'Called by the agent itself, with its install token, to fetch its own scrape fragment.',
  'GET /api/v1/schemas/webhook-payload.json': 'JSON Schema of the structured webhook payload.',
  'GET /api/v1/organizations/{org_slug}/hosts/{id}/ingest-cost':
    "A host's metric points per minute over the last 10 minutes, per scrape target (series, interval, histogram bucket share, heaviest metrics), with the organization's points-per-minute limit.",
  'POST /api/v1/organizations/{org_slug}/metrics/series/expression':
    'Run up to five metric queries ($A to $E, aggregation may be rate) and combine them with + - * /, abs() and sum(). Range and host scope are query parameters.',
  'GET /api/v1/schemas/agent-config.json': 'JSON Schema of the agent config.yaml.',
};

const tagFor = (path) => {
  const parts = path.replace('/api/v1/organizations/{org_slug}', '').split('/').filter(Boolean);
  const first = parts.find((p) => !p.startsWith('{')) ?? 'root';
  return first.replace('api', 'api').replace(/^v1$/, 'root');
};

// summaryFor turns handleGetHostGroups into "Get host groups".
const summaryFor = (handler, method, path) => {
  const words = handler
    .replace(/^handle/, '')
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1) || `${method} ${path}`;
};

const paramSchema = (name) =>
  name === 'org_slug'
    ? { type: 'string', description: 'Organization slug' }
    : name === 'id' || name === 'deliveryID'
      ? { type: 'integer', format: 'int64' }
      : { type: 'string' };

const listEndpoints = new Set([
  '/api/v1/organizations/{org_slug}/incidents',
  '/api/v1/organizations/{org_slug}/errors',
  '/api/v1/organizations/{org_slug}/hosts',
  '/api/v1/organizations/{org_slug}/services',
  '/api/v1/organizations/{org_slug}/profiles',
  '/api/v1/organizations/{org_slug}/notification-channels/{id}/deliveries',
]);

// Mirrors middleware.isReadRequest: these POSTs only read, so Idempotency skips
// them and advertising an Idempotency-Key on them would promise a replay.
const readPost = (path) =>
  path.endsWith('/explain') || path.endsWith('/metrics/series/expression');

const paths = {};

for (const route of routes) {
  const item = {};
  const params = [...route.Path.matchAll(/\{(\w+)\}/g)].map(([, name]) => ({
    name,
    in: 'path',
    required: true,
    schema: paramSchema(name),
  }));

  for (const method of route.Methods) {
    const key = `${method} ${route.Path}`;
    const handler = route.Handlers[method];
    const operation = {
      operationId: handler,
      summary: summaryFor(handler, method, route.Path),
      tags: [tagFor(route.Path)],
      responses: {
        200: { description: 'Success' },
        400: { $ref: '#/components/responses/BadRequest' },
        500: { $ref: '#/components/responses/ServerError' },
      },
    };
    if (described[key]) operation.description = described[key];
    if (params.length > 0) operation.parameters = [...params];

    if (method === 'GET' && listEndpoints.has(route.Path)) {
      operation.parameters = [
        ...(operation.parameters ?? []),
        {
          name: 'limit',
          in: 'query',
          schema: { type: 'integer', default: 50 },
          description: 'Page size. The total is returned in the X-Total-Count header.',
        },
        { name: 'offset', in: 'query', schema: { type: 'integer', default: 0 } },
      ];
      operation.responses[200] = {
        description: 'Success',
        headers: {
          'X-Total-Count': {
            description: 'Total number of rows matching the query',
            schema: { type: 'integer' },
          },
        },
        content: { 'application/json': { schema: { type: 'array', items: { type: 'object' } } } },
      };
    }

    if (
      method !== 'GET' &&
      method !== 'DELETE' &&
      !readPost(route.Path) &&
      route.Path.startsWith(`/api/v1/organizations/`)
    ) {
      operation.parameters = [
        ...(operation.parameters ?? []),
        {
          name: 'Idempotency-Key',
          in: 'header',
          required: false,
          schema: { type: 'string' },
          description:
            'Opt-in: a retry carrying the same key replays the first answer (Idempotent-Replay: true) instead of creating a second row. 409 with code idempotency_in_progress while the first call is still running.',
        },
      ];
    }

    if (method !== 'GET' && method !== 'DELETE') {
      operation.requestBody = {
        required: true,
        content: { 'application/json': { schema: { type: 'object' } } },
      };
    }

    if (!PUBLIC(route.Path)) {
      operation.responses[401] = { $ref: '#/components/responses/Unauthorized' };
    } else {
      operation.security = [];
    }

    item[method.toLowerCase()] = operation;
  }
  paths[route.Path] = item;
}

function PUBLIC(path) {
  return (
    path === '/healthz' ||
    path === '/readyz' ||
    path.startsWith('/api/v1/auth/') ||
    path.startsWith('/api/v1/webhooks/') ||
    path === '/api/v1/contact' ||
    path === '/api/v1/unsubscribe' ||
    path === '/api/v1/status' ||
    path === '/api/v1/openapi.json' ||
    path.startsWith('/api/v1/schemas/') ||
    path.startsWith('/api/v1/agents/')
  );
}

// ----- Hand-written schemas for the resources an automation tool manipulates.

const schemas = {
  AgentScrapeConfig: {
    type: 'object',
    properties: {
      scrape_config: {
        type: 'string',
        description:
          'YAML scrape fragment: a list of scrape targets, or a targets block. Empty clears it.',
      },
      updated_at: { type: 'string', format: 'date-time', nullable: true },
    },
  },
  Error: {
    type: 'object',
    properties: { error: { type: 'string' } },
    required: ['error'],
  },
  Host: {
    type: 'object',
    properties: {
      id: { type: 'integer', format: 'int64' },
      name: { type: 'string' },
      hostname: { type: 'string' },
      service: { type: 'string' },
      host_group_id: { type: 'integer', format: 'int64', nullable: true },
      status: { type: 'string' },
    },
  },
  HostGroup: {
    type: 'object',
    properties: {
      id: { type: 'integer', format: 'int64' },
      name: { type: 'string' },
      description: { type: 'string' },
    },
    required: ['name'],
  },
  Incident: {
    type: 'object',
    properties: {
      id: { type: 'integer', format: 'int64' },
      alert_rule_id: { type: 'integer', format: 'int64', nullable: true },
      service_id: { type: 'integer', format: 'int64', nullable: true },
      host_id: { type: 'integer', format: 'int64', nullable: true },
      title: { type: 'string' },
      description: { type: 'string', nullable: true },
      severity: { type: 'string', enum: ['warning', 'critical'] },
      status: { type: 'string', enum: ['open', 'acknowledged', 'resolved'] },
      started_at: { type: 'string', format: 'date-time' },
      acknowledged_at: { type: 'string', format: 'date-time', nullable: true },
      resolved_at: { type: 'string', format: 'date-time', nullable: true },
    },
  },
  IncidentStatusUpdate: {
    type: 'object',
    properties: {
      status: { type: 'string', enum: ['open', 'acknowledged', 'resolved'] },
      note: {
        type: 'string',
        description: 'Free-text note recorded with the transition.',
      },
      actor: {
        type: 'string',
        description:
          'Who requested the transition when it is not a dashboard user, e.g. "automation".',
      },
    },
    required: ['status'],
  },
  NotificationChannel: {
    type: 'object',
    properties: {
      id: { type: 'integer', format: 'int64' },
      name: { type: 'string' },
      type: { type: 'string', enum: ['email', 'slack', 'webhook', 'jsm', 'whatsapp'] },
      enabled: { type: 'boolean' },
      config: { $ref: '#/components/schemas/WebhookChannelConfig' },
    },
    required: ['name', 'type'],
  },
  WebhookChannelConfig: {
    type: 'object',
    description: 'Configuration of a webhook channel. Other channel types use their own keys.',
    properties: {
      webhook_url: { type: 'string', format: 'uri' },
      secret: {
        type: 'string',
        description: 'HMAC-SHA256 key. See the signature headers on the delivery.',
      },
      format: {
        type: 'string',
        enum: ['slack', 'structured'],
        default: 'slack',
        description:
          'slack keeps the human-readable payload; structured sends the typed event.',
      },
      headers: {
        type: 'object',
        additionalProperties: { type: 'string' },
        description: 'Extra request headers, e.g. an Authorization for a gateway.',
      },
      group_by: {
        type: 'array',
        items: { type: 'string', enum: ['host_id', 'service_id', 'alert_rule_id', 'severity'] },
        description: 'Fold a burst of alerts sharing these fields into one delivery.',
      },
      group_wait: {
        type: 'integer',
        description: 'Seconds to accumulate a burst before delivering it.',
      },
      repeat_interval: {
        type: 'integer',
        description:
          'Seconds before the same opening is delivered again. Resolutions are never suppressed.',
      },
    },
  },
  WebhookDelivery: {
    type: 'object',
    properties: {
      id: { type: 'integer', format: 'int64' },
      channel_id: { type: 'integer', format: 'int64' },
      event_id: { type: 'string' },
      event_type: { type: 'string' },
      dedup_key: { type: 'string', nullable: true },
      url: { type: 'string' },
      status_code: { type: 'integer', nullable: true },
      attempts: { type: 'integer' },
      response_body: { type: 'string', nullable: true },
      error: { type: 'string', nullable: true },
      succeeded: { type: 'boolean' },
      request_body: { type: 'string' },
      created_at: { type: 'string', format: 'date-time' },
      completed_at: { type: 'string', format: 'date-time', nullable: true },
    },
  },
  AlertRule: {
    type: 'object',
    properties: {
      id: { type: 'integer', format: 'int64' },
      name: { type: 'string' },
      type: { type: 'string' },
      target_type: { type: 'string', enum: ['any', 'service', 'host'] },
      target_id: { type: 'integer', format: 'int64', nullable: true },
      metric: { type: 'string' },
      custom_metric: { type: 'string', nullable: true },
      custom_labels: {
        type: 'array',
        items: {
          type: 'object',
          properties: { key: { type: 'string' }, value: { type: 'string' } },
        },
      },
      aggregation: { type: 'string', enum: ['avg', 'min', 'max', 'sum', 'p50', 'p75', 'p90', 'p95', 'p99'] },
      operator: { type: 'string', enum: ['gt', 'gte', 'lt', 'lte', 'eq'] },
      threshold: { type: 'number' },
      warning_threshold: { type: 'number', nullable: true },
      critical_threshold: { type: 'number', nullable: true },
      recovery_threshold: { type: 'number', nullable: true },
      duration: { type: 'integer', description: 'Evaluation window in seconds' },
      severity: { type: 'string', enum: ['warning', 'critical'] },
      enabled: { type: 'boolean' },
      channels: { type: 'array', items: { type: 'integer', format: 'int64' } },
      notify_warning: { type: 'boolean' },
      notify_critical: { type: 'boolean' },
    },
    required: ['name'],
  },
  AgentRelease: {
    type: 'object',
    properties: {
      version: { type: 'string' },
      sha256: { type: 'object', additionalProperties: { type: 'string' } },
      url: { type: 'object', additionalProperties: { type: 'string' } },
      binaries: {
        type: 'object',
        additionalProperties: {
          type: 'object',
          properties: {
            sha256: { type: 'string' },
            size: { type: 'integer' },
            updated_at: { type: 'string', format: 'date-time' },
          },
        },
      },
    },
  },
};

// Attach the schemas to the operations that carry them.
const attach = (path, method, { body, response }) => {
  const operation = paths[path]?.[method];
  if (!operation) return;
  if (body) {
    operation.requestBody = {
      required: true,
      content: { 'application/json': { schema: { $ref: `#/components/schemas/${body}` } } },
    };
  }
  if (response) {
    operation.responses[200] = {
      description: 'Success',
      content: { 'application/json': { schema: response } },
    };
  }
};

const ref = (name) => ({ $ref: `#/components/schemas/${name}` });
const arrayOf = (name) => ({ type: 'array', items: ref(name) });
const ORG = '/api/v1/organizations/{org_slug}';

attach('/api/v1/agents/latest', 'get', { response: ref('AgentRelease') });
attach(`${ORG}/host-groups`, 'get', { response: arrayOf('HostGroup') });
attach(`${ORG}/host-groups`, 'post', { body: 'HostGroup', response: ref('HostGroup') });
attach(`${ORG}/host-groups/{id}`, 'put', { body: 'HostGroup', response: ref('HostGroup') });
attach(`${ORG}/hosts`, 'get', { response: arrayOf('Host') });
attach(`${ORG}/incidents`, 'get', { response: arrayOf('Incident') });
attach(`${ORG}/incidents/{id}/status`, 'put', { body: 'IncidentStatusUpdate' });
attach(`${ORG}/incidents/dedup/{key}/status`, 'put', { body: 'IncidentStatusUpdate' });
attach(`${ORG}/alert-rules`, 'get', { response: arrayOf('AlertRule') });
attach(`${ORG}/alert-rules`, 'post', { body: 'AlertRule', response: ref('AlertRule') });
attach(`${ORG}/alert-rules/{id}`, 'put', { body: 'AlertRule', response: ref('AlertRule') });
attach(`${ORG}/notification-channels`, 'get', { response: arrayOf('NotificationChannel') });
attach(`${ORG}/notification-channels`, 'post', {
  body: 'NotificationChannel',
  response: ref('NotificationChannel'),
});
attach(`${ORG}/notification-channels/{id}`, 'put', {
  body: 'NotificationChannel',
  response: ref('NotificationChannel'),
});
attach(`${ORG}/notification-channels/{id}/deliveries`, 'get', {
  response: arrayOf('WebhookDelivery'),
});
attach(`${ORG}/notification-channels/{id}/deliveries/{deliveryID}/replay`, 'post', {
  response: ref('WebhookDelivery'),
});
attach(`${ORG}/hosts/{id}/agent-config`, 'get', { response: ref('AgentScrapeConfig') });
attach(`${ORG}/hosts/{id}/agent-config`, 'put', {
  body: 'AgentScrapeConfig',
  response: ref('AgentScrapeConfig'),
});
attach('/api/v1/agents/config', 'get', { response: ref('AgentScrapeConfig') });

const document = {
  openapi: '3.0.3',
  info: {
    title: 'Middle Monitor API',
    version: '1.0.0',
    description: [
      'REST API of the Middle Monitor observability platform.',
      '',
      'Authentication is a bearer token: a personal API token (Settings, API tokens)',
      'or an organization API key. Agent install tokens are a separate credential and',
      'are only accepted on the /agents endpoints, through the X-Install-Token header.',
      '',
      'List endpoints accept limit and offset and return the total in X-Total-Count.',
    ].join('\n'),
    license: { name: 'Proprietary' },
  },
  servers: [{ url: 'https://api.middlemonitor.io', description: 'Production' }],
  security: [{ bearerAuth: [] }],
  components: {
    securitySchemes: {
      bearerAuth: { type: 'http', scheme: 'bearer', description: 'Personal token or organization API key' },
      installToken: {
        type: 'apiKey',
        in: 'header',
        name: 'X-Install-Token',
        description: 'Agent install token, accepted only on the agent endpoints',
      },
    },
    responses: {
      BadRequest: {
        description: 'The request is invalid',
        content: { 'application/json': { schema: ref('Error') } },
      },
      Unauthorized: {
        description: 'Missing or invalid credentials',
        content: { 'application/json': { schema: ref('Error') } },
      },
      ServerError: {
        description: 'Unexpected server error',
        content: { 'application/json': { schema: ref('Error') } },
      },
    },
    schemas,
  },
  paths,
};

writeFileSync('api/openapi.json', JSON.stringify(document, null, 2) + '\n', 'utf8');
console.log(
  `[gen-openapi] api/openapi.json: ${Object.keys(paths).length} paths, ` +
    `${Object.values(paths).reduce((n, item) => n + Object.keys(item).length, 0)} operations`
);
