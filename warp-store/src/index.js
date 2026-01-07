import { L2Cache } from './l2';

/**
 * WarpClient - A high-performance, dual-layer caching store (Framework Agnostic).
 * This class implements a "Warp" strategy for data persistence and access:
 * - **L1 (Level 1)**: In-memory Map. Fast access, volatile.
 * - **L2 (Level 2)**: Persistent storage using IndexedDB.
 * Reactivity is handled via a `subscribe` method, allowing integration with
 * any framework (Vue, React, Svelte, Vanilla).
 */
class WarpClient {
    constructor() {
        // L1 Cache: Standard Map
        this.l1 = new Map();

        // L2 Cache: Persistent IndexedDB adapter
        this.l2 = new L2Cache();

        // Lease ID (Session Token)
        this.currentLease = null;

        // Subscribers for reactivity
        this.listeners = new Set();

        // Deduplication map for in-flight requests
        this.inflightRequests = new Map();
    }

    /**
     * Subscribe to store changes.
     * @param {Function} callback - Function called with { type, key, value? } on change.
     * @returns {Function} Unsubscribe function.
     */
    subscribe(callback) {
        this.listeners.add(callback);
        return () => this.listeners.delete(callback);
    }

    _notify(event) {
        this.listeners.forEach(cb => cb(event));
    }

    /**
     * Restores the session lease from IndexedDB on application startup.
     * @returns {Promise<string|null>}
     */
    async restoreSession() {
        try {
            const lease = await this.l2.getMeta('warp_lease');
            if (lease) {
                this.currentLease = lease;
                console.log("WarpClient: Session restored from L2");
                this._notify({ type: 'session_restored', value: lease });
                return lease;
            }
        } catch (e) {
            console.error("WarpClient: Failed to restore session", e);
        }
        return null;
    }

    /**
     * Initializes the store with a specific user session (lease).
     * @param {string} leaseId
     */
    async init(leaseId) {
        if (!leaseId) {
            console.warn("WarpClient: Init called without leaseId. Operating in volatile mode.");
            return;
        }

        const storedLease = await this.l2.getMeta('warp_lease');

        if (storedLease && storedLease !== leaseId) {
            console.log("WarpClient: Lease changed, purging cache.");
            await this.purge();
        }

        this.currentLease = leaseId;
        await this.l2.setMeta('warp_lease', leaseId);
        this._notify({ type: 'init', value: leaseId });
    }

    /**
     * Internal helper: Retrieves a raw entry without triggering expiration side-effects (deletion).
     * Used by SWR strategies to access stale data.
     * @param {string} key
     * @returns {Promise<{value: *, expiresAt: number|null}|null>}
     */
    async _peek(key) {
        // Check L1
        if (this.l1.has(key)) {
            return this.l1.get(key);
        }

        // Check L2
        if (this.currentLease) {
            try {
                const entry = await this.l2.get(key);
                // Verify lease, but ignore expiration for peek
                if (entry && entry.leaseId === this.currentLease) {
                    // Promote to L1 for future fast access
                    const l1Entry = {
                        value: entry.value,
                        expiresAt: entry.expiresAt
                    };
                    this.l1.set(key, l1Entry);
                    return l1Entry;
                }
            } catch (e) {
                console.error("WarpClient L2 Peek Error:", e);
            }
        }
        return null;
    }

    /**
     * Retrieves a value from the store (L1 -> L2).
     * Enforces hard expiration: if expired, the value is deleted and null is returned.
     * @param {string} key
     * @returns {Promise<*|null>}
     */
    async get(key) {
        // Check L1
        if (this.l1.has(key)) {
            const entry = this.l1.get(key);
            if (!this._isExpired(entry)) {
                return entry.value;
            } else {
                this.l1.delete(key);
                this._notify({ type: 'delete', key });
            }
        }

        // Check L2
        if (this.currentLease) {
            try {
                const entry = await this.l2.get(key);
                if (entry) {
                    // Check Lease validity
                    if (entry.leaseId !== this.currentLease) {
                        return null;
                    }

                    // Check Expiration
                    if (entry.expiresAt && Date.now() > entry.expiresAt) {
                        return null;
                    }

                    // Promote to L1
                    const l1Entry = {
                        value: entry.value,
                        expiresAt: entry.expiresAt
                    };
                    this.l1.set(key, l1Entry);

                    return entry.value;
                }
            } catch (e) {
                console.error("WarpClient L2 Error:", e);
            }
        }

        return null;
    }

    /**
     * Sets a value in the store.
     * @param {string} key
     * @param {*} value
     * @param {number} [ttlMs=0]
     */
    async set(key, value, ttlMs = 0) {
        const expiresAt = ttlMs > 0 ? Date.now() + ttlMs : null;

        this.l1.set(key, { value, expiresAt });
        this._notify({ type: 'set', key, value });

        if (this.currentLease) {
            this.l2.set(key, value, this.currentLease, ttlMs).catch(e => {
                console.error("WarpClient L2 Set Error:", e);
            });
        }
    }

    /**
     * Completely clears the store.
     */
    async purge() {
        this.l1.clear();
        await this.l2.clear();
        await this.l2.removeMeta('warp_lease');
        this.currentLease = null;
        this.inflightRequests.clear(); // Clear pending requests
        this._notify({ type: 'purge' });
    }

    /**
     * Internal helper to fetch and update the store.
     * Implements request deduplication to prevent multiple identical fetches.
     * @param {string} key 
     * @param {Function} fetcher 
     * @param {number} ttlMs 
     * @returns {Promise<*>} The fresh value
     */
    async _fetchAndSet(key, fetcher, ttlMs) {
        // Return existing promise if request is already in flight
        if (this.inflightRequests.has(key)) {
            return this.inflightRequests.get(key);
        }

        const promise = (async () => {
            try {
                const fresh = await fetcher();
                await this.set(key, fresh, ttlMs);
                return fresh;
            } catch (e) {
                console.warn("WarpClient: Background fetch failed for key:", key, e);
                throw e;
            } finally {
                this.inflightRequests.delete(key);
            }
        })();

        this.inflightRequests.set(key, promise);
        return promise;
    }

    /**
     * Loads a value with a configurable SWR (Stale-While-Revalidate) strategy.
     * Returns an object compatible with reactive UI patterns:
     * { value: any, refreshing: Promise<any>|null, fromCache: boolean }
     * Strategy:
     * 1. If cached & fresh -> Returns { value, refreshing: null }
     * 2. If cached & stale & swr=true -> Returns { value, refreshing: Promise } (Non-blocking)
     * 3. If cached & stale & swr=false -> Returns { value: null } (Blocking fetch needed)
     * 4. If missing -> Blocking fetch -> Returns { value, refreshing: null }
     * @param {string} key 
     * @param {Function} fetcher - Async function that returns the value
     * @param {Object|number} [options] - Options object or ttlMs (for backward compat)
     * @returns {Promise<{value: *, refreshing: Promise<*>|null, fromCache: boolean}>}
     */
    async load(key, fetcher, options = {}) {
        // Handle backward compatibility (ttlMs as 3rd arg)
        const opts = typeof options === 'number' ? { ttlMs: options } : options;
        const ttlMs = opts.ttlMs || 0;
        const swr = opts.swr !== false;

        // Peek at data (ignoring expiration logic to retrieve stale data)
        const entry = await this._peek(key);

        const now = Date.now();
        const hasData = entry !== null;
        const isExpired = hasData && entry.expiresAt && now > entry.expiresAt;

        const result = {
            value: hasData ? entry.value : null,
            refreshing: null,
            fromCache: hasData
        };

        // Scenario: Data exists (Fresh or Stale)
        if (hasData) {
            if (isExpired) {
                if (swr) {
                    // Stale-While-Revalidate: Return stale value, fetch in background
                    result.refreshing = this._fetchAndSet(key, fetcher, ttlMs);
                    return result;
                } else {
                    // Stale but SWR disabled: Treat as cache miss
                    result.value = null;
                    result.fromCache = false;
                }
            } else {
                // Fresh: Return immediately
                return result;
            }
        }

        // Scenario: Cache Miss (or SWR disabled on expired) - Blocking Fetch
        try {
            const fresh = await this._fetchAndSet(key, fetcher, ttlMs);
            return { value: fresh, refreshing: null, fromCache: false };
        } catch (e) {
            console.error("WarpClient: Fetch failed for key:", key, e);
            throw e;
        }
    }

    _isExpired(entry) {
        if (!entry.expiresAt) return false;
        return Date.now() > entry.expiresAt;
    }
}

// Export singleton
export const warpStore = new WarpClient();