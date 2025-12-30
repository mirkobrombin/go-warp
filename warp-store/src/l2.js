/**
 * L2Cache - A wrapper around IndexedDB for persistent storage.
 * 
 * This class handles the low-level interactions with the browser's IndexedDB,
 * providing a Promise-based API for `set`, `get`, `clear`, and metadata management.
 * It serves as the Level 2 (disk/persistent) cache for the WarpStore.
 */

const DB_NAME = 'warp-store-v1';
const DB_VERSION = 2;
const STORE_NAME = 'key-value';
const META_STORE = 'meta';

export class L2Cache {
    constructor() {
        // Initialize the database connection promise
        this.dbPromise = this._initDB();
    }

    /**
     * Opens (and upgrades if necessary) the IndexedDB database.
     * @returns {Promise<IDBDatabase>}
     * @private
     */
    _initDB() {
        return new Promise((resolve, reject) => {
            const request = indexedDB.open(DB_NAME, DB_VERSION);

            request.onerror = (event) => reject("IndexedDB error: " + event.target.errorCode);

            request.onupgradeneeded = (event) => {
                const db = event.target.result;
                // Create object store for key-value pairs if it doesn't exist
                if (!db.objectStoreNames.contains(STORE_NAME)) {
                    db.createObjectStore(STORE_NAME, { keyPath: 'key' });
                }
                // Create object store for metadata (e.g., leases) if it doesn't exist
                if (!db.objectStoreNames.contains(META_STORE)) {
                    db.createObjectStore(META_STORE, { keyPath: 'key' });
                }
            };

            request.onsuccess = (event) => {
                resolve(event.target.result);
            };
        });
    }

    // --- Data Store Methods ---

    /**
     * Retrieves a value from the persistent store.
     * @param {string} key - The key to lookup.
     * @returns {Promise<object|undefined>} The stored entry object or undefined.
     */
    async get(key) {
        const db = await this.dbPromise;
        return new Promise((resolve, reject) => {
            const transaction = db.transaction([STORE_NAME], 'readonly');
            const store = transaction.objectStore(STORE_NAME);
            const request = store.get(key);

            request.onsuccess = () => resolve(request.result);
            request.onerror = () => reject(request.error);
        });
    }

    /**
     * Saves a value to the persistent store with metadata.
     * @param {string} key - The key for the item.
     * @param {*} value - The value to store.
     * @param {string} leaseId - The current session lease ID associated with this data.
     * @param {number} ttl - Time-to-live in milliseconds (0 for no expiration).
     * @returns {Promise<void>}
     */
    async set(key, value, leaseId, ttl) {
        const db = await this.dbPromise;
        return new Promise((resolve, reject) => {
            const transaction = db.transaction([STORE_NAME], 'readwrite');
            const store = transaction.objectStore(STORE_NAME);
            const entry = {
                key,
                value,
                leaseId,
                expiresAt: ttl ? Date.now() + ttl : null
            };
            const request = store.put(entry);

            request.onsuccess = () => resolve();
            request.onerror = () => reject(request.error);
        });
    }

    /**
     * Clears all data from the key-value store.
     * @returns {Promise<void>}
     */
    async clear() {
        const db = await this.dbPromise;
        return new Promise((resolve, reject) => {
            const transaction = db.transaction([STORE_NAME], 'readwrite');
            const store = transaction.objectStore(STORE_NAME);
            const request = store.clear();

            request.onsuccess = () => resolve();
            request.onerror = () => reject(request.error);
        });
    }

    // --- Meta/Session Store Methods ---

    /**
     * Retrieves a metadata value (e.g., current lease).
     * @param {string} key - The metadata key.
     * @returns {Promise<*>} The metadata value.
     */
    async getMeta(key) {
        const db = await this.dbPromise;
        return new Promise((resolve, reject) => {
            const transaction = db.transaction([META_STORE], 'readonly');
            const store = transaction.objectStore(META_STORE);
            const request = store.get(key);

            request.onsuccess = () => resolve(request.result ? request.result.value : null);
            request.onerror = () => reject(request.error);
        });
    }

    /**
     * Sets a metadata value.
     * @param {string} key - The metadata key.
     * @param {*} value - The value to store.
     * @returns {Promise<void>}
     */
    async setMeta(key, value) {
        const db = await this.dbPromise;
        return new Promise((resolve, reject) => {
            const transaction = db.transaction([META_STORE], 'readwrite');
            const store = transaction.objectStore(META_STORE);
            const request = store.put({ key, value });

            request.onsuccess = () => resolve();
            request.onerror = () => reject(request.error);
        });
    }

    /**
     * Removes a metadata entry.
     * @param {string} key - The metadata key to remove.
     * @returns {Promise<void>}
     */
    async removeMeta(key) {
        const db = await this.dbPromise;
        return new Promise((resolve, reject) => {
            const transaction = db.transaction([META_STORE], 'readwrite');
            const store = transaction.objectStore(META_STORE);
            const request = store.delete(key);

            request.onsuccess = () => resolve();
            request.onerror = () => reject(request.error);
        });
    }
}
