export interface paths {
  '/admin/alerts': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** ops.alerts.read */
    get: operations['get_admin_alerts'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/alerts/{id}/acknowledge': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** ops.alerts.write */
    post: operations['post_admin_alerts_id_acknowledge'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/alerts/{id}/silence': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** ops.alerts.write */
    post: operations['post_admin_alerts_id_silence'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/alerts/{id}/unsilence': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** ops.alerts.write */
    post: operations['post_admin_alerts_id_unsilence'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/notification-channels': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** ops.notifications.read */
    get: operations['get_admin_notification-channels'];
    put?: never;
    /** ops.notifications.write */
    post: operations['createOpsNotificationChannel'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/notification-channels/{id}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** ops.notifications.write */
    put: operations['put_admin_notification-channels_id'];
    post?: never;
    /** ops.notifications.write */
    delete: operations['delete_admin_notification-channels_id'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/notification-channels/{id}/test': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** ops.notifications.write */
    post: operations['post_admin_notification-channels_id_test'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/alerts/{id}/deliveries': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** ops.alerts.read */
    get: operations['listOpsAlertDeliveries'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
}
export type webhooks = Record<string, never>;
export interface components {
  schemas: {
    OpsAlert: {
      /** Format: uuid */
      id: string;
      source: string;
      subject: string;
      /** @enum {string} */
      severity: 'warning' | 'critical';
      /** Format: date-time */
      first_seen: string;
      /** Format: date-time */
      last_seen: string;
      /** Format: date-time */
      acknowledged_at: string | null;
      /** Format: date-time */
      silenced_until: string | null;
      /** Format: date-time */
      resolved_at: string | null;
    };
    OpsChannel: {
      /** Format: uuid */
      id: string;
      name: string;
      enabled: boolean;
      /** @enum {string} */
      minSeverity: 'warning' | 'critical';
      configured: boolean;
    };
    OpsChannelInput: {
      name: string;
      /**
       * Format: uri
       * @description HTTPS public hostname, port 443 only. All DNS answers must be public IPv4. IPv6 is rejected.
       */
      url: string;
      bearer?: string;
      enabled: boolean;
      /** @enum {string} */
      minSeverity: 'warning' | 'critical';
    };
    OpsDelivery: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      channelId: string;
      channelName?: string | null;
      /** @enum {string} */
      status: 'pending' | 'sending' | 'sent' | 'failed' | 'cancelled';
      attempts: number;
      error?: string | null;
      /** Format: date-time */
      delivered_at?: string | null;
      /** Format: date-time */
      next_attempt_at?: string | null;
    };
  };
  responses: never;
  parameters: never;
  requestBodies: never;
  headers: never;
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  get_admin_alerts: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['OpsAlert'][];
            limit: number;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  post_admin_alerts_id_acknowledge: {
    parameters: {
      query?: {
        minutes?: number;
      };
      header?: never;
      path: {
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  post_admin_alerts_id_silence: {
    parameters: {
      query?: {
        minutes?: number;
      };
      header?: never;
      path: {
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  post_admin_alerts_id_unsilence: {
    parameters: {
      query?: {
        minutes?: number;
      };
      header?: never;
      path: {
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  'get_admin_notification-channels': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['OpsChannel'][];
            /** @description Whether encryption key is configured; otherwise channel writes/tests and automatic sends fail closed. */
            available: boolean;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  createOpsNotificationChannel: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['OpsChannelInput'];
      };
    };
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            /** Format: uuid */
            id: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  'put_admin_notification-channels_id': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['OpsChannelInput'];
      };
    };
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  'delete_admin_notification-channels_id': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  'post_admin_notification-channels_id_test': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
  listOpsAlertDeliveries: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Success */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['OpsDelivery'][];
          };
        };
      };
      /** @description Request rejected; problem code in response */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
      /** @description Request rejected; problem code in response */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': {
            code?: string;
          };
        };
      };
    };
  };
}
