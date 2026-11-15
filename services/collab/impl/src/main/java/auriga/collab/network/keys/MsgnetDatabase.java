package auriga.collab.network.keys;

import auriga.common.persistence.TenantTransactionManager;
import javax.sql.DataSource;
import org.springframework.context.ApplicationContext;
import org.springframework.core.env.Environment;

/** Combined applications must use collab roles even when IAM is the primary DataSource. */
public final class MsgnetDatabase {
  private MsgnetDatabase() {}

  public static DataSource source(ApplicationContext context, Environment env) {
    return env.getProperty("auriga.platform.combined", Boolean.class, false)
        ? context.getBean("collabDataSource", DataSource.class)
        : context.getBean(DataSource.class);
  }

  public static TenantTransactionManager manager(ApplicationContext context, Environment env) {
    return env.getProperty("auriga.platform.combined", Boolean.class, false)
        ? context.getBean("collabTransactionManager", TenantTransactionManager.class)
        : context.getBean(TenantTransactionManager.class);
  }
}
