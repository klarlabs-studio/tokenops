import { createRouter, createWebHistory, type RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "/",
    name: "home",
    component: () => import("@/views/HomeView.vue"),
    meta: { title: "Overview" },
  },
  {
    path: "/spend",
    name: "spend",
    component: () => import("@/views/SpendView.vue"),
    meta: { title: "Spend" },
  },
  {
    path: "/workflows",
    name: "workflows",
    component: () => import("@/views/WorkflowsView.vue"),
    meta: { title: "Workflows" },
  },
  {
    path: "/optimizations",
    name: "optimizations",
    component: () => import("@/views/OptimizationsView.vue"),
    meta: { title: "Optimizations" },
  },
  {
    path: "/rules",
    name: "rules",
    component: () => import("@/views/RulesView.vue"),
    meta: { title: "Rules" },
  },
  {
    path: "/events",
    name: "events",
    component: () => import("@/views/EventsView.vue"),
    meta: { title: "Events" },
  },
  {
    path: "/audit",
    name: "audit",
    component: () => import("@/views/AuditView.vue"),
    meta: { title: "Audit" },
  },
  {
    path: "/:pathMatch(.*)*",
    name: "not-found",
    component: () => import("@/views/NotFoundView.vue"),
    meta: { title: "Not found" },
  },
];

export const router = createRouter({
  history: createWebHistory(),
  routes,
});

router.afterEach((to) => {
  document.title = to.meta?.title ? `${to.meta.title} — TokenOps` : "TokenOps";
});
