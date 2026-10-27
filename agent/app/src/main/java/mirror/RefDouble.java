package mirror;

import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefDouble {
    private Field field;

    public RefDouble(Class cls, Field field) throws NoSuchFieldException {
        Field declaredField = cls.getDeclaredField(field.getName());
        this.field = declaredField;
        declaredField.setAccessible(true);
    }

    public double get(Object object) {
        try {
            return this.field.getDouble(object);
        } catch (Exception e) {
            return 0.0d;
        }
    }

    public void set(Object obj, double value) throws IllegalAccessException, IllegalArgumentException {
        try {
            this.field.setDouble(obj, value);
        } catch (Exception e) {
        }
    }
}
