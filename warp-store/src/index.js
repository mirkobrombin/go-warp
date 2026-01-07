import { L2Cache } from './l2';

/**
 * WarpClient - A high-performance, dual-layer caching store (Framework Agnostic).
 * 
 * This class implements a "Warp" strategy for data persistence and access:
 * - **L1 (Level 1)**: In-memory Map. Fast access, volatile.
 * - **L2 (Level 2)**: Persistent storage using IndexedDB.
 * 
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
     * Retrieves a value from the store (L1 -> L2).
     * @param {string} key
     * @returns {Promise<*|null>}
     */
    async get(key) {
        if (this.l1.has(key)) {
            const entry = this.l1.get(key);
            if (!this._isExpired(entry)) {

                // Track hit for LRU or just return
                return entry.value;
            } else {
                this.l1.delete(key);
                this._notify({ type: 'delete', key }); // Notify implicit expiration
            }
        }

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
        this._notify({ type: 'purge' });
    }

    async _backgroundFetch(key, fetcher, ttlMs) {
        try {
            const fresh = await fetcher();
            await this.set(key, fresh, ttlMs);
        } catch (e) {
            console.warn("WarpClient: Background fetch failed for key:", key, e);
        }
    }

    /**
     * Loads a value with Stale-While-Revalidate strategy.
     * 1. If cached, returns it immediately and refreshes in background.
     * 2. If missing/expired, waits for fetcher and sets it.
     * 
     * @param {string} key 
     * @param {Function} fetcher - Async function that returns the value
     * @param {number} [ttlMs=0] 
     * @returns {Promise<*>}
     */
    async load(key, fetcher, ttlMs = 0) {
        const cached = await this.get(key);

        if (cached !== null) {
            this._backgroundFetch(key, fetcher, ttlMs);
            return cached;
        }

        try {
            const fresh = await fetcher();
            await this.set(key, fresh, ttlMs);
            return fresh;
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
