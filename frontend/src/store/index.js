import Vue from 'vue'
import Vuex from 'vuex'
import api from '@/api'

Vue.use(Vuex)

export default new Vuex.Store({
  state: {
    limits: null,
    backendOnline: true
  },
  mutations: {
    setLimits(state, limits) {
      state.limits = limits
    },
    setBackendOnline(state, online) {
      state.backendOnline = online
    }
  },
  actions: {
    async loadLimits({ commit }) {
      try {
        const limits = await api.limits()
        commit('setLimits', limits)
        commit('setBackendOnline', true)
        return limits
      } catch (e) {
        commit('setBackendOnline', false)
        throw e
      }
    }
  }
})
